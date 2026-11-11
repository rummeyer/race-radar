package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// brokenAfter is how many failed checks in a row say a watch itself is broken: the shop may be
// slow once, but three times means it has changed, or the event is gone.
const brokenAfter = 3

// Browser is what the radar needs of the shop; tests stand in for it.
type Browser interface {
	Browse(ctx context.Context, event string, path []string) (*Page, error)
}

// Radar checks every watch in turn, every few minutes, and tells Pushover what changed.
type Radar struct {
	store  *Store
	shop   Browser
	events EventLister
	// findShop finds a race's shop on its page; tests stand in for it.
	findShop func(context.Context, string) (string, error)
	send     func(context.Context, Settings, Message) error
	log      *Activity
	wake     chan struct{}
	busy     sync.Mutex
	// When the current round started and the next is due, for the page.
	mu   sync.Mutex
	next time.Time
}

// EventLister is what the radar needs of Find my race; tests stand in for it.
type EventLister interface {
	List(ctx context.Context) ([]Event, error)
}

func NewRadar(store *Store, shop Browser, events EventLister) *Radar {
	return &Radar{store: store, shop: shop, events: events, findShop: ShopURL, send: Notify, log: NewActivity(), wake: make(chan struct{}, 1)}
}

// Run checks all watches, then waits the interval, until ctx is done.
func (r *Radar) Run(ctx context.Context) {
	for {
		r.Round(ctx)
		interval := time.Duration(r.store.Read().Settings.IntervalMinutes) * time.Minute
		r.mu.Lock()
		r.next = time.Now().Add(interval)
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		case <-r.wake:
		}
	}
}

// Next is when the next round is due.
func (r *Radar) Next() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.next
}

// Wake starts a round now, as after a change of the interval.
func (r *Radar) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Round looks for the searched races, then checks each watch not paused, one after the other.
func (r *Radar) Round(ctx context.Context) {
	if len(r.store.Read().Searches) > 0 {
		r.Search(ctx)
	}
	for _, w := range r.store.Read().Watches {
		if ctx.Err() != nil {
			return
		}
		if !w.Paused {
			r.Check(ctx, w.ID)
		}
	}
}

// Check looks at one watch's tickets now, keeps what it saw and tells of what became available.
func (r *Radar) Check(ctx context.Context, id string) {
	r.busy.Lock()
	defer r.busy.Unlock()
	data := r.store.Read()
	w := data.Find(id)
	if w == nil {
		return
	}
	r.log.Start(w.Event)
	started := time.Now()
	page, err := r.browse(ctx, w)
	r.log.Stop()
	if err == nil && len(page.Tickets) == 0 {
		err = fmt.Errorf("the shop lists no tickets after %s", strings.Join(w.Path, " › "))
	}
	var messages []Message
	var seen string
	_ = r.store.Update(func(d *Data) error {
		w := d.Find(id)
		if w == nil {
			return fmt.Errorf("gone")
		}
		messages = r.apply(w, page, err, time.Now())
		seen = summary(w)
		return nil
	})
	took := time.Since(started).Seconds()
	if errors.Is(err, errNotOnSale) {
		r.log.Add(Entry{Kind: "check", Subject: w.Event, Text: "tickets not on sale yet", Seconds: took})
	} else if err != nil {
		r.log.Add(Entry{Kind: "error", Subject: w.Event, Text: err.Error(), Seconds: took})
	} else {
		r.log.Add(Entry{Kind: "check", Subject: w.Event, Text: seen, Seconds: took})
	}
	r.notify(ctx, data.Settings, w.Event, messages)
}

// errNotOnSale is a race set up by hand whose shop is not open yet: no news, and no failure.
var errNotOnSale = errors.New("tickets not on sale yet")

// browse opens the watch's shop: for one set up by hand, found on the race's page first, and
// not open yet rather than broken while it cannot be.
func (r *Radar) browse(ctx context.Context, w *Watch) (*Page, error) {
	shop := w.URL
	if w.Planned && shop == "" {
		found, err := r.findShop(ctx, w.RacePage)
		if err != nil {
			if errors.Is(err, ErrNoShop) {
				return nil, errNotOnSale
			}
			return nil, err
		}
		shop = found
	}
	page, err := r.shop.Browse(ctx, shop, w.Path)
	if err != nil && w.Planned && page == nil {
		// No Buy Tickets, or neither choices nor tickets: the shop is there but not selling.
		return nil, errNotOnSale
	}
	if page != nil {
		page.URL = shop
	}
	return page, err
}

// notify sends each message, and logs it as sent or not.
func (r *Radar) notify(ctx context.Context, settings Settings, subject string, messages []Message) {
	for _, m := range messages {
		if err := r.send(ctx, settings, m); err != nil {
			log.Printf("%s: %v", subject, err)
			r.log.Add(Entry{Kind: "error", Subject: subject, Text: "notification not sent: " + err.Error()})
			continue
		}
		r.log.Add(Entry{Kind: "alert", Subject: subject, Text: m.Title})
	}
}

// apply takes in what a check found, and says what to notify of: tickets that became available,
// a watch that broke, or one that works again.
func (r *Radar) apply(w *Watch, page *Page, err error, now time.Time) []Message {
	w.Checked = now
	if errors.Is(err, errNotOnSale) {
		w.Waiting, w.Error, w.Failures = true, "", 0
		return nil
	}
	if err != nil {
		w.Error = err.Error()
		w.Failures++
		log.Printf("%s: check failed (%d): %v", w.Event, w.Failures, err)
		if w.Failures == brokenAfter {
			return []Message{{
				Title: "Race Radar: check broken",
				Text:  fmt.Sprintf("%s failed %d times in a row: %s", w.Event, brokenAfter, err),
				URL:   w.Shop(), URLTitle: "Open ticket shop",
			}}
		}
		return nil
	}
	var messages []Message
	if w.Planned && (w.Waiting || w.URL == "") {
		w.Waiting = false
		messages = append(messages, Message{
			Title: "Tickets on sale: " + w.Event, Text: "The shop is open: " + strings.Join(w.Path, " › ") + ".",
			URL: firstOf(page.TicketsURL, page.URL), URLTitle: "Open ticket shop", Priority: 1,
		})
	}
	if page.URL != "" && w.URL == "" {
		w.URL = page.URL
	}
	if w.Failures >= brokenAfter {
		messages = append(messages, Message{Title: "Race Radar: works again", Text: w.Event + " can be checked again."})
	}
	before := map[string]bool{}
	for _, t := range w.Tickets {
		before[t.Name] = t.Available
	}
	var fresh []string
	if w.Seen == nil {
		w.Seen = map[string]time.Time{}
	}
	for _, t := range page.Tickets {
		if t.Available && w.Wants(t.Name) && !before[t.Name] {
			fresh = append(fresh, strings.TrimSpace(t.Name+" "+t.Price))
			w.Seen[t.Name] = now
		}
	}
	w.Tickets, w.Error, w.Failures = page.Tickets, "", 0
	// Find my race's name and dates, where it gave them, read better than the shop's.
	if w.Event == "" {
		w.Event = page.Title
	}
	if w.Dates == "" {
		w.Dates = page.Dates
	}
	if page.TicketsURL != "" {
		w.TicketsURL = page.TicketsURL
	}
	log.Printf("%s: %s", w.Event, summary(w))
	if len(fresh) > 0 {
		messages = append(messages, Message{
			Title:    "Tickets available: " + w.Event,
			Text:     strings.Join(fresh, "\n") + "\nBe quick.",
			URL:      w.Shop(),
			URLTitle: "Open ticket shop",
			Priority: 1,
		})
	}
	return messages
}

// Search looks through Find my race for every search, and tells of races that are new to it or
// whose tickets went on sale.
func (r *Radar) Search(ctx context.Context) {
	r.log.Start("Find my race")
	started := time.Now()
	events, err := r.events.List(ctx)
	r.log.Stop()
	if err != nil {
		log.Printf("searching races: %v", err)
		r.log.Add(Entry{Kind: "error", Subject: "Find my race", Text: err.Error()})
		if events == nil {
			return
		}
	}
	var messages []Message
	var patterns []string
	_ = r.store.Update(func(d *Data) error {
		for _, s := range d.Searches {
			messages = append(messages, searched(s, events, time.Now())...)
			patterns = append(patterns, fmt.Sprintf("%s: %d", s.Pattern, len(s.Matches)))
		}
		return nil
	})
	r.log.Add(Entry{Kind: "search", Subject: "Find my race", Text: fmt.Sprintf("%d races; %s", len(events), strings.Join(patterns, ", ")), Seconds: time.Since(started).Seconds()})
	r.notify(ctx, r.store.Read().Settings, "Find my race", messages)
}

// searched takes in a search's matches, and says what is news: a race not seen before, or one
// whose tickets went on sale. The first look only takes note.
func searched(s *Search, events []Event, now time.Time) []Message {
	first := s.Known == nil
	if first {
		s.Known = map[string]bool{}
	}
	s.Matches, s.Checked = nil, now
	var messages []Message
	for _, e := range events {
		if !Matches(s.Pattern, e) {
			continue
		}
		s.Matches = append(s.Matches, e)
		onSale, known := s.Known[e.URL]
		s.Known[e.URL] = e.OnSale
		switch {
		case first:
		case !known:
			state := "tickets not on sale yet"
			if e.OnSale {
				state = "tickets on sale"
			}
			messages = append(messages, Message{
				Title: "New race: " + e.Name, Text: fmt.Sprintf("%s, %s: %s.", e.Dates, e.City, state),
				URL: e.URL, URLTitle: "Open race page", Priority: 1,
			})
		case e.OnSale && !onSale:
			messages = append(messages, Message{
				Title: "Tickets on sale: " + e.Name, Text: e.Dates + ". Be quick.",
				URL: e.URL, URLTitle: "Open race page", Priority: 1,
			})
		}
	}
	return messages
}

func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// summary is "HYROX MEN | Friday sold out, HYROX MEN | Saturday available", of the wanted tickets.
func summary(w *Watch) string {
	var parts []string
	for _, t := range w.Tickets {
		if w.Wants(t.Name) {
			state := "sold out"
			if t.Available {
				state = "available"
			}
			parts = append(parts, t.Name+" "+state)
		}
	}
	if len(parts) == 0 {
		return "none of the wanted tickets listed"
	}
	return strings.Join(parts, ", ")
}
