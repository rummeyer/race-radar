package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed web
var webFiles embed.FS

// Server is the settings page and its small JSON API.
type Server struct {
	store  *Store
	radar  *Radar
	shop   Browser
	events *Events
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	web, _ := fs.Sub(webFiles, "web")
	mux.Handle("GET /", http.FileServerFS(web))
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("GET /api/events", s.listEvents)
	mux.HandleFunc("POST /api/browse", s.browse)
	mux.HandleFunc("POST /api/watches", s.addWatch)
	mux.HandleFunc("PATCH /api/watches/{id}", s.changeWatch)
	mux.HandleFunc("DELETE /api/watches/{id}", s.deleteWatch)
	mux.HandleFunc("POST /api/watches/{id}/check", s.checkWatch)
	mux.HandleFunc("POST /api/searches", s.addSearch)
	mux.HandleFunc("DELETE /api/searches/{id}", s.deleteSearch)
	mux.HandleFunc("PUT /api/settings", s.saveSettings)
	mux.HandleFunc("GET /api/pushover/devices", s.devices)
	mux.HandleFunc("POST /api/test/{channel}", s.testNotification)
	return mux
}

// The page never gets the keys and passwords back, only whether they are there.
type publicSettings struct {
	IntervalMinutes int `json:"intervalMinutes"`
	Pushover        struct {
		Enabled  bool   `json:"enabled"`
		TokenSet bool   `json:"tokenSet"`
		UserSet  bool   `json:"userSet"`
		Device   string `json:"device"`
	} `json:"pushover"`
	Email struct {
		Enabled     bool   `json:"enabled"`
		Host        string `json:"host"`
		Port        int    `json:"port"`
		Username    string `json:"username"`
		PasswordSet bool   `json:"passwordSet"`
		From        string `json:"from"`
		To          string `json:"to"`
	} `json:"email"`
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	d := s.store.Read()
	if d.Watches == nil {
		d.Watches = []*Watch{}
	}
	if d.Searches == nil {
		d.Searches = []*Search{}
	}
	var p publicSettings
	p.IntervalMinutes = d.Settings.IntervalMinutes
	push, mail := d.Settings.Pushover, d.Settings.Email
	p.Pushover.Enabled, p.Pushover.TokenSet, p.Pushover.UserSet, p.Pushover.Device = push.Enabled, push.Token != "", push.User != "", push.Device
	p.Email.Enabled, p.Email.Host, p.Email.Port, p.Email.Username = mail.Enabled, mail.Host, mail.Port, mail.Username
	p.Email.PasswordSet, p.Email.From, p.Email.To = mail.Password != "", mail.From, mail.To
	entries, running, stats := s.radar.log.Snapshot()
	if entries == nil {
		entries = []Entry{}
	}
	reply(w, map[string]any{
		"settings": p,
		"watches":  d.Watches,
		"searches": d.Searches,
		"next":     s.radar.Next(),
		"running":  running,
		"log":      entries,
		"stats":    stats,
		"version":  version,
	})
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	list, err := s.events.List(r.Context())
	if err != nil && list == nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	reply(w, list)
}

// browse walks the shop's choices for the page: given an event and the choices so far, what
// comes next.
func (s *Server) browse(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL  string   `json:"url"`
		Path []string `json:"path"`
	}
	if !read(w, r, &in) {
		return
	}
	shop, err := ShopURL(r.Context(), in.URL)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	page, err := s.shop.Browse(r.Context(), shop, in.Path)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	page.URL = shop
	if isRacePage(in.URL) {
		page.RacePage = in.URL
	}
	reply(w, page)
}

func (s *Server) addWatch(w http.ResponseWriter, r *http.Request) {
	var in Watch
	if !read(w, r, &in) {
		return
	}
	planned := in.Planned && isRacePage(in.RacePage) && len(in.Path) > 0
	if !planned && (!strings.HasPrefix(in.URL, "https://") || len(in.Path) == 0 && len(in.Wanted) == 0) {
		fail(w, http.StatusBadRequest, errors.New("choose a race and its tickets first"))
		return
	}
	watch := &Watch{
		ID: newID(), Event: in.Event, Dates: in.Dates, URL: in.URL, RacePage: in.RacePage, TicketsURL: in.TicketsURL,
		Path: in.Path, Steps: in.Steps, Wanted: in.Wanted, Tickets: in.Tickets, Created: time.Now(),
		Planned: planned, Days: in.Days, Waiting: planned,
	}
	if planned {
		watch.URL, watch.Wanted, watch.Tickets = "", nil, nil
	}
	if err := s.store.Update(func(d *Data) error { d.Watches = append(d.Watches, watch); return nil }); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	// A first look at once, not in up to an interval's time.
	go s.radar.Check(context.Background(), watch.ID)
	reply(w, watch)
}

func (s *Server) changeWatch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Paused   *bool    `json:"paused"`
		Wanted   []string `json:"wanted"`
		RacePage *string  `json:"racePage"`
	}
	if !read(w, r, &in) {
		return
	}
	err := s.store.Update(func(d *Data) error {
		watch := d.Find(r.PathValue("id"))
		if watch == nil {
			return errNotFound
		}
		if in.Paused != nil {
			watch.Paused = *in.Paused
		}
		if in.Wanted != nil {
			watch.Wanted = in.Wanted
		}
		if in.RacePage != nil && (*in.RacePage == "" || isRacePage(*in.RacePage)) {
			watch.RacePage = *in.RacePage
		}
		return nil
	})
	done(w, err)
}

func (s *Server) deleteWatch(w http.ResponseWriter, r *http.Request) {
	err := s.store.Update(func(d *Data) error {
		for i, watch := range d.Watches {
			if watch.ID == r.PathValue("id") {
				d.Watches = append(d.Watches[:i], d.Watches[i+1:]...)
				return nil
			}
		}
		return errNotFound
	})
	done(w, err)
}

func (s *Server) checkWatch(w http.ResponseWriter, r *http.Request) {
	if s.store.Read().Find(r.PathValue("id")) == nil {
		fail(w, http.StatusNotFound, errNotFound)
		return
	}
	s.radar.Check(r.Context(), r.PathValue("id"))
	reply(w, s.store.Read().Find(r.PathValue("id")))
}

func (s *Server) addSearch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pattern string `json:"pattern"`
	}
	if !read(w, r, &in) {
		return
	}
	pattern := strings.TrimSpace(in.Pattern)
	if strings.Trim(pattern, "* ") == "" {
		fail(w, http.StatusBadRequest, errors.New("enter a name to look for, Berlin say"))
		return
	}
	search := &Search{ID: newID(), Pattern: pattern, Created: time.Now()}
	// What is there already is taken note of now, so that only what comes after is news.
	if events, err := s.events.List(r.Context()); err == nil {
		searched(search, events, time.Now())
	}
	if err := s.store.Update(func(d *Data) error { d.Searches = append(d.Searches, search); return nil }); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	reply(w, search)
}

func (s *Server) deleteSearch(w http.ResponseWriter, r *http.Request) {
	err := s.store.Update(func(d *Data) error {
		for i, search := range d.Searches {
			if search.ID == r.PathValue("id") {
				d.Searches = append(d.Searches[:i], d.Searches[i+1:]...)
				return nil
			}
		}
		return errNotFound
	})
	done(w, err)
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IntervalMinutes int `json:"intervalMinutes"`
		Pushover        struct {
			Enabled bool   `json:"enabled"`
			Token   string `json:"token"`
			User    string `json:"user"`
			Device  string `json:"device"`
		} `json:"pushover"`
		Email struct {
			Enabled  bool   `json:"enabled"`
			Host     string `json:"host"`
			Port     int    `json:"port"`
			Username string `json:"username"`
			Password string `json:"password"`
			From     string `json:"from"`
			To       string `json:"to"`
		} `json:"email"`
	}
	if !read(w, r, &in) {
		return
	}
	if in.IntervalMinutes < 2 || in.IntervalMinutes > 1440 {
		fail(w, http.StatusBadRequest, errors.New("check every 2 to 1440 minutes"))
		return
	}
	changed := false
	err := s.store.Update(func(d *Data) error {
		changed = d.Settings.IntervalMinutes != in.IntervalMinutes
		d.Settings.IntervalMinutes = in.IntervalMinutes
		p, e := &d.Settings.Pushover, &d.Settings.Email
		p.Enabled, p.Device = in.Pushover.Enabled, in.Pushover.Device
		// Empty secrets keep what is saved: the page never sees them to send them back.
		keep(&p.Token, in.Pushover.Token)
		keep(&p.User, in.Pushover.User)
		e.Enabled, e.Host, e.Port = in.Email.Enabled, strings.TrimSpace(in.Email.Host), in.Email.Port
		e.Username, e.From, e.To = strings.TrimSpace(in.Email.Username), strings.TrimSpace(in.Email.From), strings.TrimSpace(in.Email.To)
		keep(&e.Password, in.Email.Password)
		return nil
	})
	if changed {
		s.radar.Wake()
	}
	done(w, err)
}

func keep(saved *string, given string) {
	if given = strings.TrimSpace(given); given != "" {
		*saved = given
	}
}

func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Read().Settings.Pushover.Devices(r.Context())
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	reply(w, list)
}

func (s *Server) testNotification(w http.ResponseWriter, r *http.Request) {
	settings := s.store.Read().Settings
	m := Message{Title: "Race Radar: test", Text: "This is how a ticket alert arrives.", URL: FindMyRace, URLTitle: "Find my race"}
	var err error
	switch r.PathValue("channel") {
	case "pushover":
		err = settings.Pushover.Send(r.Context(), m)
	case "email":
		err = settings.Email.Send(m)
	default:
		err = errNotFound
	}
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	reply(w, map[string]bool{"ok": true})
}

var errNotFound = errors.New("no such watch")

// isRacePage says whether a link is to a race's page on hyrox.com.
func isRacePage(link string) bool {
	u, err := url.Parse(link)
	return err == nil && u.Scheme == "https" && strings.TrimPrefix(u.Hostname(), "www.") == "hyrox.com" && strings.HasPrefix(u.Path, "/event/")
}

func read(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(into); err != nil {
		fail(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func done(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotFound):
		fail(w, http.StatusNotFound, err)
	case err != nil:
		fail(w, http.StatusInternalServerError, err)
	default:
		reply(w, map[string]bool{"ok": true})
	}
}

func fail(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
