package util

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"time"

	"github.com/pubnub/go/gae/messaging"

	"appengine"
	"appengine/datastore"
	"appengine/user"
)

// Page Definition
type Page struct {
	Pubber  Pubber
	Content interface{}
}

type Pubber struct {
	Id       int64 `datastore:"-"`
	First    string
	Username string
	Email    string
	Links    []Link
}

func (p *Pubber) Key(c appengine.Context) *datastore.Key {
	return datastore.NewKey(c, "User", p.Email, 0, nil)
}

func (p *Pubber) Save(c appengine.Context) (*Pubber, error) {
	k, err := datastore.Put(c, p.Key(c), p)
	if err != nil {
		return nil, err
	}
	p.Id = k.IntID()
	return p, nil
}

// Blog Definition
type Link struct {
	Id      int64 `datastore:"-"`
	Short   string
	Created time.Time
	URL     string
	Data    LinkData
}

func (l *Link) UrlKey(c appengine.Context) *datastore.Key {
	return datastore.NewKey(c, "Link", l.URL, 0, nil)
}

func (l *Link) Key(c appengine.Context) *datastore.Key {
	return datastore.NewKey(c, "Link", l.Short, 0, nil) //l.UrlKey(c))
}

func (l *Link) Save(c appengine.Context) (*Link, error) {
	k, err := datastore.Put(c, l.Key(c), l)
	if err != nil {
		return nil, err
	}
	l.Id = k.IntID()
	return l, nil
}

// Use array of bytes for JSON string
type LinkData struct {
	Clicks    int    `json:"clicks"`
	Referers  []byte `json:"referers"`
	Languages []byte `json:"languages"`
	Browsers  []byte `json:"browsers"`
	OSs       []byte `json:"oss"`
}

type Click struct {
	IP       string `json:"ip"`
	Referer  string `json:"referer"`
	Language string `json:"language"`
	Browser  string `json:"browser"`
	OS       string `json:"os"`
}

var (
	EmptyPubber Pubber = Pubber{}
	EmptyLink   Link   = Link{}
	alphabetLen        = 62
)

// Converts count to the range [a-zA-Z0-9]
func ShortenLink(c appengine.Context, link string) string {
	count, err := GetCount(c)
	if err != nil {
		return ""
	}
	incrementCount(c)
	logVal := math.Log2(float64(count)) / math.Log2(float64(alphabetLen))
	buffLen := int(math.Ceil(logVal))
	if buffLen <= 0 {
		buffLen = 1
	}
	var buff []byte = make([]byte, buffLen)
	for i := 0; i < buffLen; i++ {
		buff[i] = letterCode(count % alphabetLen)
		count /= alphabetLen
	}
	if err != nil {
		c.Infof("Shorten Err: %v\n", err)
		return ""
	}
	c.Infof("Count: %d, Buff: %d - %s\n", count, buffLen, string(buff))
	return string(buff)
}

// Alphabet [a-zA-Z0-9]
func letterCode(n int) byte {
	b := byte(n)
	if n < 26 {
		return 'a' + b
	} else if n < 26*2 {
		return 'A' + b
	} else {
		return b - 26*2
	}
}

func GetPubber(c appengine.Context) (*Pubber, error) {
	u := user.Current(c)
	if u == nil {
		return &EmptyPubber, errors.New("Not Signed In")
	}
	var pbr Pubber
	var pubber *Pubber = &pbr
	key := UserKey(c)
	err := datastore.Get(c, key, &pbr)
	if err != nil {
		c.Infof("No pubber in datastore - %v\n", err)
		pubber = &Pubber{First: u.ID, Username: u.String(), Email: u.Email}
		pubber, err = pubber.Save(c)
		if err != nil {
			c.Infof("Error: %v\n", err)
			return nil, err
		}
		return pubber, nil
	}
	c.Infof("Im HERE %v\n", pubber)
	pubber.Id = key.IntID()
	if pubber.First == "" {
		pubber.First = u.String()
	}
	return pubber, nil
}

// UserKey to identify App Engine User
func UserKey(c appengine.Context) *datastore.Key {
	u := user.Current(c)
	if u == nil {
		return datastore.NewKey(c, "User", "Anonymous", 0, nil)
	}
	return datastore.NewKey(c, "User", u.Email, 0, nil)
}

func GetLink(c appengine.Context, id string) *Link {
	var link Link
	key := datastore.NewKey(c, "Link", id, 0, nil)
	err := datastore.Get(c, key, &link)
	if err != nil {
		c.Infof("Link Error - %v\n", err)
		return &EmptyLink
	}
	link.Id = key.IntID()
	return &link
}

type shardCounter struct {
	Count int
}

const (
	numShards = 20
	shardKind = "counterShard"
)

func GetCount(c appengine.Context) (int, error) {
	total := 0
	q := datastore.NewQuery(shardKind)
	for t := q.Run(c); ; {
		var s shardCounter
		_, err := t.Next(&s)
		if err == datastore.Done {
			break
		}
		if err != nil {
			return total, err
		}
		total += s.Count
	}
	return total, nil
}

func incrementCount(c appengine.Context) error {
	return datastore.RunInTransaction(c, func(c appengine.Context) error {
		shardName := fmt.Sprintf("shard%d", rand.Intn(numShards))
		key := datastore.NewKey(c, shardKind, shardName, 0, nil)
		var s shardCounter
		err := datastore.Get(c, key, &s)
		if err != nil && err != datastore.ErrNoSuchEntity {
			return err
		}
		s.Count++
		_, err = datastore.Put(c, key, &s)
		return err
	}, nil)
}

var (
	publishKey   = "pub-c-7fad26fe-6c38-4940-b9c3-fbd19a9633af" // Use your PubKey
	subscribeKey = "sub-c-30c17e1a-0007-11e5-a8ef-0619f8945a4f" // Use your SubKey
	secretKey    = ""
)

// PubMessage used to store data for PubNub Publishing
type PubMessage struct {
	Data  LinkData `json:"data"`
	Click Click    `json:"click"`
}

// Publish a message using a PubNub client
func Publish(c appengine.Context, w http.ResponseWriter, r *http.Request, ch string, m *PubMessage) {
	uuid := "Server" // Will make custom UUID if empty string
	message, err := json.Marshal(m)
	if err != nil {
		c.Infof("Bad JSON!")
		return
	}

	errorChannel := make(chan []byte)
	successChannel := make(chan []byte)
	timeoutChannel := make(chan bool, 1) // Boolean channel of size 1

	// func New(context, uuid, writer, request, publishKey, subscribeKey, secretKey, cipher, ssl bool) *Pubnub
	pubInstance := messaging.New(c, uuid, w, r, publishKey, subscribeKey, secretKey, "", false)
	go pubInstance.Publish(c, w, r, ch, message, successChannel, errorChannel)

	go func() {
		time.Sleep(1 * time.Second) // Quit after 1 seconds
		timeoutChannel <- true
	}()

	for {
		select {
		case success, ok := <-successChannel:
			if !ok {
				c.Infof("success!OK")
				break
			}
			if string(success) != "[]" {
				c.Infof("success:", string(success))
			}
			return
		case failure, ok := <-errorChannel:
			if !ok {
				c.Infof("fail1:", string("failure"))
				break
			}
			if string(failure) != "[]" {
				c.Infof("fail:", string(failure))
			}
			return
		case <-timeoutChannel:
			c.Infof("timeout:", string("NonSubscribeTimeout"))
			return
		}
	}
}
