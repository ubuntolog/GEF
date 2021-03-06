package server

import (
	"bytes"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/EUDAT-GEF/GEF/gefserver/db"
)

func (s *Server) decorate(fn func(http.ResponseWriter, *http.Request), actionType string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		logRequest(r)
		user, err := s.getCurrentUser(r)
		if err != nil {
			log.Println("User error: ", err)
		}

		allow, closefn := signalEvent(actionType, user, r)
		if !allow {
			Response{w}.DirectiveError()
		} else {
			defer closefn()
			fn(w, r)
		}
	}
}

type eventSystem struct {
	address string
	ID      uint64
}

var eventSys eventSystem

type eventPayload struct {
	Event       event                  `json:"event"`
	User        *db.User               `json:"user"`
	Resource    resource               `json:"resource"`
	Environment map[string]interface{} `json:"environment"`
}

type event struct {
	ID        uint64 `json:"id"`
	Time      string `json:"time"`   // iso string
	Action    string `json:"action"` // one of: "DataAnalysis"...
	Preceding bool   `json:"preceding"`
}

type resource struct {
	URL    string `json:"uri"`    // "/api/jobs",
	Method string `json:"method"` // "POST",
}

// InitEventSystem initializes the event system, or disables it if the address
// is empty
func InitEventSystem(address string) {
	if address == "" {
		log.Println("Event system disabled")
		return
	}
	if !strings.HasSuffix(address, "/") {
		address += "/"
	}
	eventSys = eventSystem{
		address: address + "api/events",
	}
}

func (es eventSystem) dispatch(action string, user *db.User, r *http.Request, preceding bool) bool {
	if es.address == "" {
		return true
	}

	payload := eventPayload{
		Event: event{
			ID:        atomic.AddUint64(&es.ID, 1),
			Time:      time.Now().Format(time.RFC3339),
			Action:    action,
			Preceding: preceding,
		},
		User: user,
		Resource: resource{
			URL:    r.RequestURI,
			Method: r.Method,
		},
		Environment: map[string]interface{}{},
	}
	json, err := json.Marshal(payload)
	if err != nil {
		log.Printf("event system: json marshal ERROR: %#v\n", err)
	} else {
		resp, err := http.Post(es.address, "application/json", bytes.NewBuffer(json))
		if err != nil {
			if e, ok := err.(*url.Error); ok {
				if e, ok := e.Err.(*net.OpError); ok {
					log.Printf("event system: post url net ERROR: %#v\n", e)
				}
				log.Printf("event system: post url ERROR: %#v\n", e)
			}
			log.Printf("event system: post ERROR: %#v\n", err)
			return true
		}
		if 400 <= resp.StatusCode && resp.StatusCode < 500 {
			log.Printf("event system: post client ERROR: %#v\n", resp)
			return false
		}
	}
	return true
}

func signalEvent(action string, user *db.User, r *http.Request) (allow bool, closefn func()) {
	fn := func() {}
	ret := eventSys.dispatch(action, user, r, true)
	if ret {
		fn = func() {
			eventSys.dispatch(action, user, r, false)
		}
	}
	return ret, fn
}
