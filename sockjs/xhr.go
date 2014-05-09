package sockjs

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (h *handler) xhrSend(rw http.ResponseWriter, req *http.Request) {
	if req.Body == nil {
		httpError(rw, "Payload expected.", http.StatusInternalServerError)
		return
	}
	var messages []string
	err := json.NewDecoder(req.Body).Decode(&messages)
	if err == io.EOF {
		httpError(rw, "Payload expected.", http.StatusInternalServerError)
		return
	}
	if _, ok := err.(*json.SyntaxError); ok || err == io.ErrUnexpectedEOF {
		httpError(rw, "Broken JSON encoding.", http.StatusInternalServerError)
		return
	}
	sessionID, err := h.parseSessionID(req.URL)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess, ok := h.sessions[sessionID]; !ok {
		http.NotFound(rw, req)
	} else {
		_ = sess.accept(messages...)                                 // TODO(igm) reponse with SISE in case of err?
		rw.Header().Set("content-type", "text/plain; charset=UTF-8") // Ignored by net/http (but protocol test complains), see https://code.google.com/p/go/source/detail?r=902dc062bff8
		rw.WriteHeader(http.StatusNoContent)
	}
}

var cFrame = closeFrame(2010, "Another connection still open")

func (h *handler) xhrPoll(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("content-type", "application/javascript; charset=UTF-8")
	sess, _ := h.sessionByRequest(req) // TODO(igm) add err handling, although err should not happen as handler should not pass req in that case
	receiver := newXhrReceiver(rw, 1)
	if err := sess.attachReceiver(receiver); err != nil {
		receiver.sendFrame(cFrame)
		return
	}

	select {
	case <-receiver.doneNotify():
	case <-receiver.interruptedNotify():
	}
}

var xhrStreamingPrelude = strings.Repeat("h", 2048)

func (h *handler) xhrStreaming(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("content-type", "application/javascript; charset=UTF-8")
	fmt.Fprintf(rw, "%s\n", xhrStreamingPrelude)
	rw.(http.Flusher).Flush()

	sess, _ := h.sessionByRequest(req)
	receiver := newXhrReceiver(rw, h.options.ResponseLimit)
	if err := sess.attachReceiver(receiver); err != nil {
		receiver.sendFrame(cFrame)
		return
	}

	select {
	case <-receiver.doneNotify():
	case <-receiver.interruptedNotify():
	}
}

func (h *handler) sessionByRequest(req *http.Request) (*session, error) {
	h.sessionsMux.Lock()
	defer h.sessionsMux.Unlock()
	sessionID, err := h.parseSessionID(req.URL)
	if err != nil {
		return nil, err
	}
	sess, exists := h.sessions[sessionID]
	if !exists {
		sess = newSession(h.options.DisconnectDelay, h.options.HeartbeatDelay)
		h.sessions[sessionID] = sess
		if h.handlerFunc != nil {
			go h.handlerFunc(sess)
		}
		go func() {
			<-sess.closedNotify()
			h.sessionsMux.Lock()
			delete(h.sessions, sessionID)
			h.sessionsMux.Unlock()
		}()
	}
	return sess, nil
}
