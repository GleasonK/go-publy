package publy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gleasonk/util"
	"github.com/gorilla/mux"

	"appengine"
	"appengine/user"
)

var templates = make(map[string]*template.Template)
var baseDir = "github.com/gleasonk/publy/"

func init() {
	buildTemplates()
	rtr := mux.NewRouter()
	rtr.StrictSlash(true)
	rtr.HandleFunc("/", root)
	rtr.HandleFunc("/l/{link:[a-zA-Z0-9]*}", visitHandler)
	rtr.HandleFunc("/login", loginHandler)
	rtr.HandleFunc("/logout", logoutHandler)
	rtr.HandleFunc("/user", makeHandler(showProfile))
	rtr.HandleFunc("/link/{link:[a-zA-Z0-9]}", linkHandler)
	// rtr.HandleFunc("/user/update", makeHandler(updateUser))
	rtr.HandleFunc("/new", newLink)
	http.Handle("/", rtr)
}

func buildTemplates() {
	funcMap := template.FuncMap{
		"byteToMap": func(b []byte) map[string]int {
			var m map[string]int
			err := json.Unmarshal(b, &m)
			if err != nil {
				m = make(map[string]int)
			}
			return m
		},
	}
	templates["index"] = template.Must(template.ParseFiles(baseDir+"tmpl/index.html", baseDir+"tmpl/base.html"))
	templates["user"] = template.Must(template.ParseFiles(baseDir+"tmpl/user.html", baseDir+"tmpl/base.html"))
	templates["new"] = template.Must(template.ParseFiles(baseDir+"tmpl/new.html", baseDir+"tmpl/base.html"))
	templates["link"] = template.Must(template.New("").Funcs(funcMap).ParseFiles(baseDir+"tmpl/link.html", baseDir+"tmpl/base.html"))
}

func root(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	pubr, _ := util.GetPubber(c)
	page := util.Page{Pubber: *pubr}
	buff := renderTemplate(w, "index", page)
	if buff != nil {
		w.Write(buff)
	}
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	u := user.Current(c)
	if u != nil {
		http.Redirect(w, r, "/user/", http.StatusFound)
		return
	}
	url, err := user.LoginURL(c, r.URL.String())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Location", url)
	w.WriteHeader(http.StatusFound)
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	u := user.Current(c)
	if u == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	url, err := user.LogoutURL(c, "/")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Location", url)
	w.WriteHeader(http.StatusFound)
}

func showProfile(w http.ResponseWriter, r *http.Request, c appengine.Context) {
	pbr, _ := util.GetPubber(c)
	/*for _, link := range pbr.Links {
		link.Short = link.Key(c).Encode()
		c.Infof("Encoded: %s\n", link.Key(c).Encode())
	} //Consider Encoding for privacy? */
	page := util.Page{Pubber: *pbr}
	buff := renderTemplate(w, "user", page)
	if buff != nil {
		w.Write(buff)
	}
}

func newLink(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	pbr, _ := util.GetPubber(c)
	switch r.Method {
	case "POST":
		url := r.FormValue("url")
		linkId := util.ShortenLink(c, url)
		link := util.Link{
			Short:   linkId,
			Created: time.Now(),
			URL:     url,
			Data:    util.LinkData{},
		}
		l, err := link.Save(c)
		if err != nil {
			c.Infof("NewLink Error: %v\n", err)
		}
		if pbr.Email != "" { // TODO: Change to be list of link-keys
			pbr.Links = append(pbr.Links, *l)
			pbr.Save(c)
		}
		c.Infof("Link: %v\n", link)
		fwdUrl := fmt.Sprintf("/link/%s", l.Short)
		http.Redirect(w, r, fwdUrl, http.StatusFound)
		break
	case "GET":
		page := util.Page{Pubber: *pbr}
		buff := renderTemplate(w, "new", page)
		if buff != nil {
			w.Write(buff)
		}
	}
}

func linkHandler(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	pbr, _ := util.GetPubber(c)
	vars := mux.Vars(r)
	linkId := vars["link"]
	link := util.GetLink(c, linkId)
	c.Infof("LINK: %v\n", util.EmptyLink)
	page := util.Page{Pubber: *pbr, Content: *link}
	buff := renderTemplate(w, "link", page)
	if buff != nil {
		w.Write(buff)
	}
}

func visitHandler(w http.ResponseWriter, r *http.Request) {
	var (
		referers  map[string]int
		languages map[string]int
		browsers  map[string]int
		oss       map[string]int
	)

	c := appengine.NewContext(r)
	vars := mux.Vars(r)
	linkId := vars["link"]
	if linkId == "" {
		http.Redirect(w, r, "/", http.StatusFound)
	}
	link := util.GetLink(c, linkId)
	if link.URL == "" {
		http.Redirect(w, r, "/", http.StatusFound)
	}
	// Update and publish
	header := r.Header
	lang := strings.ToLower(strings.Split(header.Get("Accept-Language"),
		",")[0])
	ref := r.Referer()

	if ref == "" {
		ref = "(not set)"
	}
	ip := getIP(r)
	ua := r.UserAgent()
	browser := browserDetect(ua)
	os := osDetect(ua)

	// Update the LinkData
	data := &link.Data
	data.Clicks++

	err := json.Unmarshal(data.Referers, &referers)
	if err != nil {
		c.Infof("JSON Err1: %v\n", err)
		referers = make(map[string]int)
	}
	refJson, err := updateLDMap(referers, ref)
	if err == nil {
		data.Referers = refJson
	}

	err = json.Unmarshal(data.Languages, &languages)
	if err != nil {
		languages = make(map[string]int)
	}
	langJson, err := updateLDMap(languages, lang)
	if err == nil {
		data.Languages = langJson
	}

	err = json.Unmarshal(data.Browsers, &browsers)
	if err != nil {
		browsers = make(map[string]int)
	}
	browJson, err := updateLDMap(browsers, browser)
	if err == nil {
		data.Browsers = browJson
	}

	err = json.Unmarshal(data.OSs, &oss)
	if err != nil {
		oss = make(map[string]int)
	}
	ossJson, err := updateLDMap(oss, os)
	if err == nil {
		data.OSs = ossJson
	}

	link.Save(c)

	// Publish the Click with PubNub
	click := util.Click{
		IP:       ip,
		Referer:  ref,
		Language: lang,
		Browser:  browser,
		OS:       os,
	}
	pm := &util.PubMessage{
		Data:  link.Data,
		Click: click,
	}
	util.Publish(c, w, r, linkId, pm)
	http.Redirect(w, r, link.URL, http.StatusFound)
}

func makeHandler(fn func(http.ResponseWriter, *http.Request, appengine.Context)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := appengine.NewContext(r)
		u := user.Current(c)
		if u == nil {
			url, err := user.LoginURL(c, r.URL.String())
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Location", url)
			w.WriteHeader(http.StatusFound)
			return
		}
		fn(w, r, c)
	}
}

func renderTemplate(w http.ResponseWriter, tmpl string, page interface{}) []byte {
	buffer := new(bytes.Buffer)
	err := templates[tmpl].ExecuteTemplate(buffer, "base", page)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil
	}
	return buffer.Bytes()
}

func serve404(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "Not Found")
}

func updateLDMap(m map[string]int, val string) ([]byte, error) {
	_, ok := m[val]
	if ok {
		m[val]++
	} else {
		m[val] = 1
	}
	json, err := json.Marshal(m)
	if err != nil {
		return []byte("{}"), errors.New("Bad JSON")
	}
	return json, nil
}

func getIP(r *http.Request) string {
	if ipProxy := r.Header.Get("X-FORWARDED-FOR"); len(ipProxy) > 0 {
		return ipProxy
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}

// Detect the OS by the user agent string
// TODO: Get the version using regex
func osDetect(ua string) string {
	switch {
	case strings.Contains(ua, "iPhone"):
		return "iOS"
	case strings.Contains(ua, "Android"):
		return "Android"
	case strings.Contains(ua, "Macintosh"):
		return "Macintosh"
	case strings.Contains(ua, "Windows Phone"):
		return "Windows Mobile"
	case strings.Contains(ua, "Windows"):
		return "Windows"
	case strings.Contains(ua, "Chilkat"):
		return "Chilkat"
	}
	return "(unknown)"
}

// Detect browser from UA
func browserDetect(ua string) string {
	switch {
	case strings.Contains(ua, "Safari"):
		if strings.Contains(ua, "Chrome") {
			return "Chrome"
		} else if strings.Contains(ua, "Chromium") {
			return "Chromium"
		} else {
			return "Safari"
		}
	case strings.Contains(ua, "Chromium"):
		return "Chromium"
	case strings.Contains(ua, "IEMobile"):
		return "IEMobile"
	case strings.Contains(ua, "MSIE"):
		return "Internet Explorer"
	case strings.Contains(ua, "Opera") || strings.Contains(ua, "OPR"):
		return "Opera"
	}
	return "(unknown)"
}
