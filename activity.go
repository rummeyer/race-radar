package main

import (
	"sync"
	"time"
)

// Entry is one thing the radar did, for the dashboard's log.
type Entry struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"` // check, search, alert, error
	Subject string    `json:"subject"`
	Text    string    `json:"text"`
	Seconds float64   `json:"seconds,omitempty"`
}

// Running is the check going on now.
type Running struct {
	Subject string    `json:"subject"`
	Since   time.Time `json:"since"`
}

// Stats sum up the radar's work since it started.
type Stats struct {
	Started   time.Time `json:"started"`
	Checks    int       `json:"checks"`
	Failures  int       `json:"failures"`
	Alerts    int       `json:"alerts"`
	LastAlert time.Time `json:"lastAlert,omitzero"`
}

// Activity keeps the latest entries in memory: the dashboard shows what happened lately, the
// journal keeps the rest.
type Activity struct {
	mu      sync.Mutex
	entries []Entry
	running *Running
	stats   Stats
}

const keepEntries = 200

// NewActivity is an empty log, its stats counting from now.
func NewActivity() *Activity {
	return &Activity{stats: Stats{Started: time.Now()}}
}

// Add logs what the radar did, and counts it.
func (a *Activity) Add(e Entry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e.At.IsZero() {
		e.At = time.Now()
	}
	switch e.Kind {
	case "check":
		a.stats.Checks++
	case "error":
		a.stats.Checks++
		a.stats.Failures++
	case "alert":
		a.stats.Alerts++
		a.stats.LastAlert = e.At
	}
	a.entries = append(a.entries, e)
	if len(a.entries) > keepEntries {
		a.entries = a.entries[len(a.entries)-keepEntries:]
	}
}

// Start says what is being checked now.
func (a *Activity) Start(subject string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.running = &Running{Subject: subject, Since: time.Now()}
}

// Stop says the check is over.
func (a *Activity) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.running = nil
}

// Snapshot is the newest entries first, what runs now, and the sums.
func (a *Activity) Snapshot() ([]Entry, *Running, Stats) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entries := make([]Entry, len(a.entries))
	for i, e := range a.entries {
		entries[len(a.entries)-1-i] = e
	}
	var running *Running
	if a.running != nil {
		copied := *a.running
		running = &copied
	}
	return entries, running, a.stats
}
