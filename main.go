package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
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
)

var (
	authKey  = os.Getenv("POUSSETACHES_AUTH_KEY")
	basePath = "poussetaches_data"
	client   = &http.Client{}
	wg       = sync.WaitGroup{}
	tasksMu  = sync.Mutex{}
	tasks    = []*task{}
)

const (
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
}

type task struct {
	ID string `json:"id"`

	URL      string `json:"url"`
	Payload  []byte `json:"payload"`
	Expected int    `json:"expected"`

	NextRun int64 `json:"next_run"`
	Tries   int   `json:"tries"`

	LastErrorBody       []byte `json:"last_error_body"`
	LastErrorStatusCode int    `json:"last_error_status_code"`
}

type taskPayload struct {
	Payload []byte `json:"payload"`
	Tries   int    `json:"tries"`
	ReqID   string `json:"req_id"`
}

func (t *task) execute() error {
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
		return success(t)
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
	task := tasks[0]
	if time.Now().UnixNano() < task.NextRun {
		return nil
	}
	tasks = tasks[1:]
	return task
}

func loadTasks() error {
	waiting, err := loadDir("waiting")
	if err != nil {
		return err
	}
	for _, t := range waiting {
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

func newTask(u string, p []byte, expected int) *task {
	// TODO: dump to disk
	t := &task{
		ID:       newID(6),
		URL:      u,
		Payload:  p,
		Expected: expected,
		NextRun:  time.Now().UnixNano(),
	}
	if t.Expected == 0 {
		t.Expected = 200
	}
	dumpTask(t, "waiting")
	appendTask(t)
	return t
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
				if err := t.execute(); err != nil {
					// TODO see what happen to the task in this case
					log.Printf("failed to execute task: %+v: %v\n", t, err)
				}
				log.Printf("Task done: %+v in %v\n", t, time.Since(start))
			}
			time.Sleep(200 * time.Millisecond)
			continue L
		}
	}
	fmt.Printf("worker stopped\n")
}

func main() {
	for _, where := range []string{"dead", "waiting", "success"} {
		if err := os.MkdirAll(filepath.Join(basePath, where), 0700); err != nil {
			panic(err)
		}
	}
	if err := loadTasks(); err != nil {
		panic(err)
	}

	go func() {
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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
			log.Printf("received new task %+v\n", nt)
			t := newTask(nt.URL, nt.Payload, nt.Expected)
			w.Header().Set("Poussetaches-Task-ID", t.ID)
			w.WriteHeader(http.StatusCreated)
		})
		for _, where := range []string{"dead", "waiting", "success"} {
			http.HandleFunc("/"+where, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
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
		}

		log.Println("Start HTTP API at :7991")
		http.ListenAndServe(":7991", nil)
	}()

	log.Println("poussetaches starting...")
	stop := make(chan struct{}, 1)
	go worker(stop)

	cs := make(chan os.Signal, 1)
	signal.Notify(cs, os.Interrupt,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	<-cs
	stop <- struct{}{}
	wg.Wait()
	log.Println("Shutdown")
}
