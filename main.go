package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	mrand "math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/robfig/cron"
	"golang.org/x/time/rate"
)

var (
	authKey  = os.Getenv("POUSSETACHES_AUTH_KEY")
	basePath = "poussetaches_data"
	client   = &http.Client{}
	wg       = sync.WaitGroup{}
	tasksMu  = sync.Mutex{}
	tasks    = []*task{}
	paused   = true
	inFlight = 0
	limiter  *rate.Limiter
	schedIdx = map[string]struct{}{}
)

const (
	maxSuccess = 1000 // Only keep the last 1000 done/succeeded tasks
	maxRetries = 12
)

var retries = []int{
	1, 4, 16, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304,
}

// "randomize" the retries delay
func addJitter(i int) int {
	// add +/- 30% randomly
	jitter := float64(mrand.Int63n(30)) / 100
	if mrand.Int63n(1) == 0 {
		return int(math.Round((1.0 - jitter) * float64(i)))
	}
	return int(math.Round((1.0 + jitter) * float64(i)))
}

// Generate a new random ID (hex-encoded)
func newID(n int) string {
	dat := make([]byte, n)
	if _, err := rand.Read(dat); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", dat)
}

type newTaskInput struct {
	URL      string `json:"url"`
	Payload  []byte `json:"payload"`
	Expected int    `json:"expected"`
	Schedule string `json:"schedule,omitempty"`
	Delay    int    `json:"delay,omitempty"`
}

type task struct {
	ID string `json:"id"`

	URL      string `json:"url"`
	Payload  []byte `json:"payload"`
	Expected int    `json:"expected"`
	Schedule string `json:"schedule"`

	NextScheduledRun int64 `json:"next_scheduled_run"`
	NextRun          int64 `json:"next_run"`
	Tries            int   `json:"tries"`

	LastRun             int64  `json:"last_run"`
	LastErrorBody       []byte `json:"last_error_body"`
	LastErrorStatusCode int    `json:"last_error_status_code"`
}

type taskPayload struct {
	Payload []byte `json:"payload"`
	Tries   int    `json:"tries"`
	ReqID   string `json:"req_id"`
}

func (t *task) execute() error {
	tasksMu.Lock()
	inFlight++
	tasksMu.Unlock()
	defer func() {
		tasksMu.Lock()
		inFlight--
		tasksMu.Unlock()
	}()
	t.Tries++
	reqID := newID(6)
	tp := &taskPayload{
		Payload: t.Payload,
		Tries:   t.Tries,
		ReqID:   reqID,
	}
	p, err := json.Marshal(tp)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", t.URL, bytes.NewBuffer(p))
	if err != nil {
		return err
	}
	req.Header.Set("Poussetaches-Auth-Key", authKey)
	log.Printf("req=%+v", req)

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("req failed=%v\n", err)
		return failure(t, -1, []byte(err.Error()))
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode == t.Expected {
		if err := success(t); err != nil {
			return err
		}
		if t.Schedule != "" {
			return reschedule(t)
		}
		return nil
	}

	return failure(t, resp.StatusCode, body)
}

func appendTask(t *task) {
	tasksMu.Lock()
	defer tasksMu.Unlock()
	tasks = append(tasks, t)
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].NextRun < tasks[j].NextRun })
}

func getNextTask() *task {
	tasksMu.Lock()
	defer tasksMu.Unlock()
	if len(tasks) == 0 {
		return nil
	}
	if paused {
		return nil
	}
	task := tasks[0]
	if time.Now().UnixNano() < task.NextRun {
		return nil
	}
	tasks = tasks[1:]
	return task
}

func loadTasks() error {
	tasksMu.Lock()
	tasks = []*task{}

	waiting, err := loadDir("waiting")
	if err != nil {
		return err
	}
	tasksMu.Unlock()

	for _, t := range waiting {
		if t.Schedule != "" {
			// Remove the scheduled task
			log.Printf("dropping scheduled task %+v\n", t)
			if err := unlinkTask(t, "waiting"); err != nil {
				return err
			}
			delete(schedIdx, t.ID)
			continue
		}
		appendTask(t)
	}
	return nil
}

func loadDir(where string) ([]*task, error) {
	files, err := ioutil.ReadDir(filepath.Join(basePath, where))
	if err != nil {
		return nil, err
	}
	tasks := []*task{}
	for _, f := range files {
		content, err := ioutil.ReadFile(filepath.Join(basePath, where, f.Name()))
		if err != nil {
			return nil, err
		}
		t := &task{}
		if err := json.Unmarshal(content, t); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}

	return tasks, nil
}

func newTask(u string, p []byte, expected int, sched string, mdelay int) *task {
	nextRun := time.Now().Add(time.Duration(mdelay) * time.Minute)
	tid := newID(16)
	if sched != "" {
		h := sha1.New()
		io.WriteString(h, u)
		h.Write(p)
		io.WriteString(h, sched)

		schedKey := fmt.Sprintf("%x", h.Sum(nil))
		log.Printf("sched key=%s\n", schedKey)

		if _, ok := schedIdx[schedKey]; ok {
			return &task{ID: schedKey}
		}

		schedIdx[schedKey] = struct{}{}
		tid = schedKey

		// Setup the initial next run if this is a cron/scheduled task
		schedule, err := cron.Parse(sched)
		if err != nil {
			panic(err)
		}
		nextRun = schedule.Next(nextRun)

	}
	t := &task{
		ID:               tid,
		URL:              u,
		Payload:          p,
		Expected:         expected,
		Schedule:         sched,
		NextRun:          nextRun.UnixNano(),
		NextScheduledRun: nextRun.UnixNano(),
	}
	if t.Expected == 0 {
		t.Expected = 200
	}
	if err := dumpTask(t, "waiting"); err != nil {
		panic(err)
	}
	appendTask(t)
	return t
}

func reschedule(t *task) error {
	lastRun := time.Unix(0, t.NextScheduledRun)
	// Setup the initial next run if this is a cron/scheduled task
	schedule, err := cron.Parse(t.Schedule)
	if err != nil {
		return err
	}
	t.NextScheduledRun = schedule.Next(lastRun).UnixNano()
	t.NextRun = t.NextScheduledRun
	t.Tries = 0
	t.LastErrorBody = nil
	t.LastErrorStatusCode = 0
	if err := dumpTask(t, "waiting"); err != nil {
		panic(err)
	}
	appendTask(t)
	return nil
}

func success(t *task) error {
	if err := unlinkTask(t, "waiting"); err != nil {
		return err
	}
	return dumpTask(t, "success")
}

func dead(t *task) error {
	if err := unlinkTask(t, "waiting"); err != nil {
		return err
	}
	return dumpTask(t, "dead")
}

func unlinkTask(t *task, where string) error {
	return os.Remove(filepath.Join(basePath, where, t.ID))
}

func dumpTask(t *task, where string) error {
	js, err := json.Marshal(t)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(filepath.Join(basePath, where, t.ID), js, 0644)
}

func failure(t *task, status int, serr []byte) error {
	t.LastErrorStatusCode = status
	t.LastErrorBody = serr
	if t.Tries+1 < maxRetries {
		t.NextRun = time.Now().Add(time.Duration(addJitter(retries[t.Tries-1])) * time.Second).UnixNano()
		if err := dumpTask(t, "waiting"); err != nil {
			return err
		}
		appendTask(t)
	} else {
		return dead(t)
	}
	return nil
}

func worker(stop <-chan struct{}) {
	wg.Add(1)
	defer wg.Done()
L:
	for {
		select {
		case <-stop:
			break L
		default:
			t := getNextTask()
			start := time.Now()
			if t != nil {
				r := limiter.Reserve()
				if !r.OK() {
					log.Println("Not allowed to act!")
					time.Sleep(200 * time.Millisecond)
				}
				log.Printf("worker sleeping for %v\n", r.Delay())
				time.Sleep(r.Delay())

				t.LastRun = start.UnixNano()
				if err := t.execute(); err != nil {
					// TODO see what happen to the task in this case
					log.Printf("failed to execute task: %+v: %v\n", t, err)
				}
				log.Printf("Task done: %+v in %v\n", t, time.Since(start))
			} else {
				time.Sleep(200 * time.Millisecond)
			}
			continue L
		}
	}
	fmt.Printf("worker stopped\n")
}

func removeOldSuccess() error {
	success, err := loadDir("success")
	if err != nil {
		return err
	}
	if len(success) < maxSuccess {
		return nil
	}
	// Sort by last run desc
	sort.Slice(success, func(i, j int) bool { return success[i].LastRun > success[j].LastRun })
	for _, t := range success[maxSuccess:] {
		if err := unlinkTask(t, "success"); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	for _, where := range []string{"dead", "waiting", "success"} {
		if err := os.MkdirAll(filepath.Join(basePath, where), 0700); err != nil {
			panic(err)
		}
	}
	if err := removeOldSuccess(); err != nil {
		panic(err)
	}
	if err := loadTasks(); err != nil {
		panic(err)
	}

	go func() {
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "GET" {
				tasksMu.Lock()
				defer tasksMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(&map[string]interface{}{
					"paused":    paused,
					"in_flight": inFlight,
				}); err != nil {
					panic(err)
				}
				return
			}

			if r.Method != "POST" {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}

			decoder := json.NewDecoder(r.Body)
			nt := &newTaskInput{}
			err := decoder.Decode(&nt)
			if err != nil {
				panic(err)
			}
			if nt.Schedule != "" {
				// Ensure the spec is valid
				if _, err := cron.Parse(nt.Schedule); err != nil {
					panic(err)
				}
			}
			log.Printf("received new task %+v\n", nt)
			t := newTask(nt.URL, nt.Payload, nt.Expected, nt.Schedule, nt.Delay)
			w.Header().Set("Poussetaches-Task-ID", t.ID)
			w.WriteHeader(http.StatusCreated)
		})
		http.HandleFunc("/cron", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case "GET":
				tasksMu.Lock()
				defer tasksMu.Unlock()
				tasks, err := loadDir("waiting")
				if err != nil {
					panic(err)
				}
				cronTasks := []*task{}
				for _, t := range tasks {
					if t.Schedule == "" {
						continue
					}
					cronTasks = append(cronTasks, t)
				}

				sort.Slice(cronTasks, func(i, j int) bool { return cronTasks[i].NextRun < cronTasks[j].NextRun })
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(&map[string]interface{}{
					"tasks": cronTasks,
				}); err != nil {
					panic(err)
				}

			case "DELETE":
				if err := loadTasks(); err != nil {
					panic(err)

				}
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		})

		http.HandleFunc("/pause", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			tasksMu.Lock()
			defer tasksMu.Unlock()
			paused = true
		})
		http.HandleFunc("/resume", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			tasksMu.Lock()
			defer tasksMu.Unlock()
			paused = false
		})
		for _, where := range []string{"dead", "waiting", "success"} {
			func(where string) {
				http.HandleFunc("/"+where, func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" {
						w.WriteHeader(http.StatusMethodNotAllowed)
						return
					}
					tasksMu.Lock()
					defer tasksMu.Unlock()
					tasks, err := loadDir(where)
					if err != nil {
						panic(err)
					}

					sort.Slice(tasks, func(i, j int) bool { return tasks[i].NextRun < tasks[j].NextRun })
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(&map[string]interface{}{
						"tasks": tasks,
					}); err != nil {
						panic(err)
					}
				})
			}(where)
		}

		log.Println("Start HTTP API at :7991")
		http.ListenAndServe(":7991", nil)
	}()

	log.Println("poussetaches starting in...")

	// 3 reqs/second with a burst of 5
	limiter = rate.NewLimiter(rate.Limit(3), 5)
	workers := 2
	stop := make(chan struct{}, workers)
	for i := 0; i < workers; i++ {
		go worker(stop)
	}

	// Wait until the server shut down
	cs := make(chan os.Signal, 1)
	signal.Notify(cs, os.Interrupt,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	<-cs
	for i := 0; i < workers; i++ {
		stop <- struct{}{}
	}
	wg.Wait()
	log.Println("Shutdown")
}
