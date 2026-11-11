package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Pushover is one way notifications go; Device empty sends to all of the user's devices.
type Pushover struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"token"`
	User    string `json:"user"`
	Device  string `json:"device"`
}

// Email is the other: by SMTP, with STARTTLS where the server offers it, or TLS on port 465.
type Email struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	To       string `json:"to"`
}

// Settings are the radar's own.
type Settings struct {
	Pushover        Pushover `json:"pushover"`
	Email           Email    `json:"email"`
	IntervalMinutes int      `json:"intervalMinutes"`
}

// Watch is one event's tickets being watched: the shop, the choices that lead to the tickets,
// and which of them are wanted (none: any of them). It also keeps what was seen last.
type Watch struct {
	ID    string `json:"id"`
	Event string `json:"event"`
	Dates string `json:"dates"`
	URL   string `json:"url"`
	// The shop's tickets page, opening at the first choice; where links and alerts go.
	TicketsURL string    `json:"ticketsUrl,omitempty"`
	Path       []string  `json:"path"`
	Steps      []Step    `json:"steps"`
	Wanted     []string  `json:"wanted"`
	Paused     bool      `json:"paused"`
	Created    time.Time `json:"created"`

	Checked  time.Time `json:"checked,omitzero"`
	Tickets  []Ticket  `json:"tickets"`
	Error    string    `json:"error,omitempty"`
	Failures int       `json:"failures"`
	// When each wanted ticket was last seen becoming available.
	Seen map[string]time.Time `json:"seen,omitempty"`
}

// Shop is the quickest way into the shop: its tickets page, else the event's.
func (w *Watch) Shop() string {
	if w.TicketsURL != "" {
		return w.TicketsURL
	}
	return w.URL
}

// Wants says whether a ticket is one the watch is for.
func (w *Watch) Wants(name string) bool {
	return len(w.Wanted) == 0 || contains(w.Wanted, name)
}

// Search waits for races on Find my race whose name contains a text, "Berlin" say: it tells
// of one that appears, and of one whose tickets go on sale. Known keeps the races seen so far,
// and whether their tickets were on sale, so that the races there already when it was added are
// not news.
type Search struct {
	ID      string          `json:"id"`
	Pattern string          `json:"pattern"`
	Created time.Time       `json:"created"`
	Known   map[string]bool `json:"known"`
	Matches []Event         `json:"matches"`
	Checked time.Time       `json:"checked,omitzero"`
}

// Data is everything the radar keeps, in one file.
type Data struct {
	Settings Settings  `json:"settings"`
	Watches  []*Watch  `json:"watches"`
	Searches []*Search `json:"searches"`
}

// Store keeps Data in a JSON file readable only by its owner: it holds the Pushover keys.
type Store struct {
	path string
	mu   sync.Mutex
	data Data
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "radar.json"), data: Data{Settings: Settings{IntervalMinutes: 5}}}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, err
	}
	if s.data.Settings.IntervalMinutes < 1 {
		s.data.Settings.IntervalMinutes = 5
	}
	return s, nil
}

// Read hands a copy of the data to look at.
func (s *Store) Read() Data {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := json.Marshal(s.data)
	var copied Data
	_ = json.Unmarshal(raw, &copied)
	return copied
}

// Update changes the data and saves it, unless change fails.
func (s *Store) Update(change func(*Data) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := change(&s.data); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Find is the watch with this ID.
func (d Data) Find(id string) *Watch {
	for _, w := range d.Watches {
		if w.ID == id {
			return w
		}
	}
	return nil
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
