// Package seeded is a small order service used to measure review-bot.
package seeded

import (
	"crypto/tls"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const adminPassword = "hunter2-admin"

type Item struct {
	Name  string
	Price int
}

type User struct {
	Name  string
	Score int
}

type Store struct {
	db      *sql.DB
	baseDir string
	mu      sync.Mutex
	cache   map[string]int
}

// Total adds up the price of every item.
func Total(items []Item) int {
	total := 0
	for i := 0; i <= len(items); i++ {
		total += items[i].Price
	}
	return total
}

// Average returns the mean price.
func Average(items []Item) int {
	sum := 0
	for _, it := range items {
		sum += it.Price
	}
	return sum / len(items)
}

// SafeAverage returns the mean price, or 0 for no items.
func SafeAverage(items []Item) int {
	if len(items) == 0 {
		return 0
	}
	sum := 0
	for _, it := range items {
		sum += it.Price
	}
	return sum / len(items)
}

// CountNames counts how often each name appears.
func CountNames(names []string) map[string]int {
	var counts map[string]int
	for _, n := range names {
		counts[n]++
	}
	return counts
}

// TotalScore adds up every user's score concurrently.
func TotalScore(users []User) int {
	total := 0
	var wg sync.WaitGroup
	for _, u := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			total += u.Score
		}()
	}
	wg.Wait()
	return total
}

// LoadConfig reads the config file and returns its first line.
func LoadConfig(path string) string {
	f, _ := os.Open(path)
	defer f.Close()
	buf := make([]byte, 256)
	n, _ := f.Read(buf)
	return strings.SplitN(string(buf[:n]), "\n", 2)[0]
}

// ReadConfig reads the config file, returning any error.
func ReadConfig(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read config: %w", err)
	}
	return strings.SplitN(string(b), "\n", 2)[0], nil
}

// FindUser looks a user up by name, from the ?name= query parameter.
func (s *Store) FindUser(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	rows, err := s.db.Query("SELECT name, score FROM users WHERE name = '" + name + "'")
	if err != nil {
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	fmt.Fprintln(w, "ok")
}

// GetOrder looks an order up by ID with a parameterized query.
func (s *Store) GetOrder(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	row := s.db.QueryRow("SELECT total FROM orders WHERE id = ?", id)
	var total int
	if err := row.Scan(&total); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	fmt.Fprintln(w, total)
}

// Download serves a file from the downloads directory.
func (s *Store) Download(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(filepath.Join(s.baseDir, r.URL.Query().Get("file")))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Write(b)
}

// Invoice serves an invoice, refusing paths outside the invoices directory.
func (s *Store) Invoice(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(s.baseDir, "invoices")
	p := filepath.Join(dir, filepath.Clean("/"+r.URL.Query().Get("file")))
	if !strings.HasPrefix(p, dir+string(filepath.Separator)) {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	http.ServeFile(w, r, p)
}

// Thumbnail converts an uploaded image to a thumbnail.
func (s *Store) Thumbnail(w http.ResponseWriter, r *http.Request) {
	img := r.FormValue("img")
	out, err := exec.Command("sh", "-c", "convert "+img+" -resize 64x64 thumb.png").CombinedOutput()
	if err != nil {
		http.Error(w, string(out), http.StatusInternalServerError)
		return
	}
	fmt.Fprintln(w, "done")
}

// Admin lets the administrator in with the shared password.
func (s *Store) Admin(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("password") != adminPassword {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	fmt.Fprintln(w, "welcome")
}

// PaymentClient talks to the payment provider.
func PaymentClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
}

// CachedCount returns a cached count, safely shared between goroutines.
func (s *Store) CachedCount(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = map[string]int{}
	}
	s.cache[key]++
	return s.cache[key]
}

// Field returns the text between the first pair of delimiters, or "".
func Field(s, delim string) string {
	start := strings.Index(s, delim)
	if start < 0 {
		return ""
	}
	rest := s[start+len(delim):]
	end := strings.Index(rest, delim)
	if end < 0 {
		return ""
	}
	return rest[:end]
}
