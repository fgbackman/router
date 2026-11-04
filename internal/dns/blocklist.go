package dns

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Blocklist matches a domain and all of its subdomains.
type Blocklist struct{ set map[string]struct{} }

func NewBlocklist() *Blocklist { return &Blocklist{set: map[string]struct{}{}} }

func (b *Blocklist) Len() int { return len(b.set) }

func (b *Blocklist) Add(domain string) {
	d := strings.Trim(strings.ToLower(strings.TrimSpace(domain)), ".")
	if d == "" || !strings.Contains(d, ".") {
		return
	}
	b.set[d] = struct{}{}
}

// Match reports whether name or any parent domain is blocked.
func (b *Blocklist) Match(name string) bool {
	if b == nil || len(b.set) == 0 {
		return false
	}
	for {
		if _, ok := b.set[name]; ok {
			return true
		}
		i := strings.IndexByte(name, '.')
		if i < 0 {
			return false
		}
		name = name[i+1:]
	}
}

var skipHosts = map[string]bool{
	"localhost": true, "localhost.localdomain": true, "local": true,
	"broadcasthost": true, "ip6-localhost": true, "ip6-loopback": true, "0.0.0.0": true,
}

// Parse reads hosts-file ("0.0.0.0 ads.example") or plain ("ads.example")
// lines. Adblock-style "||ads.example^" lines are accepted too.
func (b *Blocklist) Parse(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexAny(line, "#!"); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		d := f[0]
		if _, err := netip.ParseAddr(d); err == nil {
			if len(f) < 2 {
				continue
			}
			d = f[1]
		}
		d = strings.TrimSuffix(strings.TrimPrefix(d, "||"), "^")
		if skipHosts[strings.ToLower(d)] {
			continue
		}
		b.Add(d)
	}
	return sc.Err()
}

// LoadSources fills a blocklist from files and URLs. Downloads are cached in
// cacheDir so a restart without internet still blocks. Errors for individual
// sources are returned alongside whatever loaded successfully.
func LoadSources(ctx context.Context, sources, domains []string, cacheDir string) (*Blocklist, []error) {
	bl := NewBlocklist()
	for _, d := range domains {
		bl.Add(d)
	}
	var errs []error
	for _, src := range sources {
		if err := loadSource(ctx, bl, src, cacheDir); err != nil {
			errs = append(errs, fmt.Errorf("blocklist %s: %w", src, err))
		}
	}
	return bl, errs
}

func loadSource(ctx context.Context, bl *Blocklist, src, cacheDir string) error {
	if !strings.HasPrefix(src, "http://") && !strings.HasPrefix(src, "https://") {
		f, err := os.Open(src)
		if err != nil {
			return err
		}
		defer f.Close()
		return bl.Parse(f)
	}
	sum := sha256.Sum256([]byte(src))
	cache := filepath.Join(cacheDir, hex.EncodeToString(sum[:8])+".txt")
	if err := download(ctx, src, cache); err != nil {
		f, cerr := os.Open(cache)
		if cerr != nil {
			return err
		}
		defer f.Close()
		if perr := bl.Parse(f); perr != nil {
			return perr
		}
		return fmt.Errorf("%w (using cached copy)", err)
	}
	f, err := os.Open(cache)
	if err != nil {
		return err
	}
	defer f.Close()
	return bl.Parse(f)
}

func download(ctx context.Context, url, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "router-blocklist/1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, 64<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
