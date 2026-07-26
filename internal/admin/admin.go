// Package admin lets the unprivileged nodeosd request privileged operations
// (install/switch the node implementation, change pruning, self-update).
//
// Mechanism: nodeosd writes a command file into <dataDir>/admin/; a root-owned
// systemd path unit (installed by install.sh) watches the directory and runs
// /usr/local/bin/nodeos-admin, which validates and executes the command,
// streaming output to <job>.log and finishing with <job>.done or <job>.fail.
// No sudo involved, so nodeosd keeps NoNewPrivileges hardening.
package admin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const pollInterval = time.Second

type Client struct {
	dir string // <dataDir>/admin

	mu  sync.Mutex
	job *Job
}

type Job struct {
	Name    string    `json:"name"`
	Started time.Time `json:"started"`
	Running bool      `json:"running"`
	OK      bool      `json:"ok"`
	Error   string    `json:"error,omitempty"`
	Log     []string  `json:"log"`
}

func New(dataDir string) *Client {
	return &Client{dir: filepath.Join(dataDir, "admin")}
}

// Available reports whether the root helper side is set up: install.sh drops
// a marker file the helper owns. On dev machines this is false and the API
// returns a friendly error instead.
func (c *Client) Available() bool {
	_, err := os.Stat(filepath.Join(c.dir, ".helper-ready"))
	return err == nil
}

// Start enqueues a command for the root helper and follows it in the
// background. One job at a time.
func (c *Client) Start(name string, args ...string) (*Job, error) {
	if !c.Available() {
		return nil, fmt.Errorf("admin helper not installed on this machine (run deploy/install.sh)")
	}
	for _, a := range args {
		if strings.ContainsAny(a, "\n\r") {
			return nil, fmt.Errorf("invalid argument")
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.job != nil && c.job.Running {
		return nil, fmt.Errorf("another admin job (%s) is still running", c.job.Name)
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return nil, err
	}
	id := fmt.Sprintf("job-%d", time.Now().UnixNano())
	body := strings.Join(append([]string{name}, args...), "\n") + "\n"
	// write via temp+rename so the path unit never sees a half-written file
	tmp := filepath.Join(c.dir, id+".tmp")
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, filepath.Join(c.dir, id+".cmd")); err != nil {
		return nil, err
	}
	job := &Job{Name: name, Started: time.Now(), Running: true}
	c.job = job
	go c.follow(id, job)
	return job, nil
}

// follow polls the helper's log/result files until the job finishes.
func (c *Client) follow(id string, job *Job) {
	logPath := filepath.Join(c.dir, id+".log")
	deadline := time.Now().Add(30 * time.Minute)
	for {
		time.Sleep(pollInterval)
		if b, err := os.ReadFile(logPath); err == nil {
			lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
			c.mu.Lock()
			job.Log = lines
			c.mu.Unlock()
		}
		if _, err := os.Stat(filepath.Join(c.dir, id+".done")); err == nil {
			c.finish(id, job, true, "")
			return
		}
		if _, err := os.Stat(filepath.Join(c.dir, id+".fail")); err == nil {
			c.finish(id, job, false, "helper reported failure — see log")
			return
		}
		if time.Now().After(deadline) {
			c.finish(id, job, false, "timed out after 30 minutes")
			return
		}
	}
}

func (c *Client) finish(id string, job *Job, ok bool, errMsg string) {
	c.mu.Lock()
	job.Running = false
	job.OK = ok
	job.Error = errMsg
	c.mu.Unlock()
	// keep the log around for a bit, clean markers so the dir stays tidy
	for _, suf := range []string{".done", ".fail"} {
		os.Remove(filepath.Join(c.dir, id+suf))
	}
	time.AfterFunc(10*time.Minute, func() {
		os.Remove(filepath.Join(c.dir, id+".log"))
	})
}

// Current returns a copy of the most recent job (nil if none ran yet).
func (c *Client) Current() *Job {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.job == nil {
		return nil
	}
	cp := *c.job
	cp.Log = append([]string(nil), c.job.Log...)
	if len(cp.Log) > 40 {
		cp.Log = cp.Log[len(cp.Log)-40:]
	}
	return &cp
}
