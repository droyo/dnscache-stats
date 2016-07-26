package main

import (
	"bufio"
	"flag"
	"html/template"
	"io"
	"log"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/paulhammond/tai64"

	"aqwari.net/io/tailpipe"
)

const (
	MetricsBufferMax   = 100
	QueryTrackingLimit = 100000
)

var (
	interval     = flag.Duration("i", time.Minute, "time between data points")
	nameTemplate = flag.String("m",
		"servers.{{.Hostname}}.dnscache.{{.Metric}}",
		"naming scheme for metrics")
	inputFile      = flag.String("f", "", "tail this file instead of stdin")
	verbose        = flag.Bool("v", false, "print detailed information to stderr")
	timestampedLog = flag.Bool("tai64", false, "log input contains tai64n timestamps")
)

var Hostname, Service string

// Most of these heuristics assume daemontools
func guessServiceName() string {
	pwd, _ := os.Getwd()

	// Example: /service/dnscache/log/run
	if path.Base(pwd) == "log" {
		return path.Base(path.Dir(pwd))
	}

	// Example: /var/log/dnscache
	dir, el := path.Split(pwd)
	for len(el) > 0 {
		if strings.Contains(el, "dnscache") {
			return el
		}
		dir, el = path.Split(dir)
	}

	// If we were to return "" here, we would be
	// creating invalid metric names.
	return "main"
}

// A ticker that aligns returned times on regular multiples
// of interval. Leaks a goroutine, so do not use in a library
func alignedTicker(d time.Duration) <-chan time.Time {
	aligned := make(chan time.Time)
	go func() {
		for t := range time.Tick(d) {
			aligned <- t.Truncate(d)
		}
	}()
	return aligned
}

func verbosef(fmt string, args ...interface{}) {
	if *verbose {
		log.Printf(fmt, args...)
	}
}

type metric struct {
	Metric string
	Time   time.Time
	Value  int
}

// For metric name templates
func (m metric) Hostname() string {
	return Hostname
}

type stats struct {
	cacheHits   int
	dropped     int
	queries     map[string]time.Time
	nqueries    int
	latency     time.Duration
	maxLatency  time.Duration
	servfail    int
	outgoing    int
	activeUDP   int
	activeTCP   int
	cacheMotion struct{ start, end int }
}

type logReader struct {
	src         io.Reader
	dst         chan metric
	interval    time.Duration
	timestamped bool
	done        chan struct{}

	mu *sync.Mutex // protects following members
	stats
}

// There are two main run modes; from timestamped
// input and non-timestamped input. Timestamped is
// useful because it allows you to convert old data to
// fill in gaps.
func (l *logReader) run() {
	var lastTime, now time.Time
	var err error

	if !l.timestamped {
		go l.sampleRun()
	}
	scanner := bufio.NewScanner(l.src)
	for scanner.Scan() {
		line := strings.Fields(scanner.Text())
		if len(line) < 1 {
			continue
		}
		if l.timestamped {
			now, err = tai64.ParseTai64n(line[0])
			if err != nil {
				verbosef("bad timestamp %q: %v", line[0], err)
				continue
			}
			line = line[1:]
			if lastTime.IsZero() {
				lastTime = now.Truncate(l.interval)
			} else if now.Sub(lastTime) >= l.interval {
				// Do this instead of lastTime = now so we get
				// evenly spaced data points
				lastTime = now.Truncate(l.interval)
				l.sample(lastTime)
			}
		} else {
			now = time.Now()
		}
		l.parseLine(now, line)
	}
	if scanner.Err() != nil {
		verbosef("finished processing log: %v", scanner.Err())
	}
	if l.timestamped {
		close(l.dst)
	} else {
		close(l.done)
	}
}

// the run() method blocks on reading input, and therefore cannot
// send data at regular intervals. rather than implement read timeouts,
// when running on non-timestamped input, sampling is done in a
// separate goroutine.
func (l *logReader) sampleRun() {
	tick := alignedTicker(l.interval)

Loop:
	for {
		select {
		case <-l.done:
			break Loop
		case ts := <-tick:
			l.sample(ts)
		}
	}
	close(l.dst)
}

func (l *logReader) parseLine(ts time.Time, line []string) {
	if len(line) < 3 {
		return
	}
	event, args := line[0], line[1:]
	l.mu.Lock()
	defer l.mu.Unlock()

	switch event {
	case "cached":
		l.cacheHits++
	case "drop":
		l.dropped++
		delete(l.queries, args[0])
	case "query":
		if len(l.queries) < QueryTrackingLimit {
			l.queries[args[0]] = ts
			l.nqueries++
		}
	case "servfail":
		l.servfail++
	case "sent":
		if v, ok := l.queries[args[0]]; ok {
			latency := ts.Sub(v)
			if latency > l.maxLatency {
				l.maxLatency = latency
			}
			l.latency += latency
			delete(l.queries, args[0])
		}
	case "stats":
		if len(args) < 4 {
			break
		}
		if v, err := strconv.Atoi(args[1]); err == nil {
			if l.cacheMotion.start == 0 {
				l.cacheMotion.start = v
			}
			l.cacheMotion.end = v
		}
		if activeUDP, err := strconv.Atoi(args[2]); err == nil {
			if activeUDP > l.activeUDP {
				l.activeUDP = activeUDP
			}
		}
		if activeTCP, err := strconv.Atoi(args[3]); err == nil {
			if activeTCP > l.activeTCP {
				l.activeTCP = activeTCP
			}
		}
	case "tx":
		l.outgoing++
	}
}

func (l *logReader) sample(ts time.Time) {
	var metrics []metric
	l.mu.Lock()
	{
		metrics = []metric{
			{"queries", ts, l.nqueries},
			{"cache_motion", ts, l.cacheMotion.end - l.cacheMotion.start},
			{"udp_active", ts, l.activeUDP},
			{"tcp_active", ts, l.activeTCP},
			{"cache_hits", ts, l.cacheHits},
			{"drop", ts, l.dropped},
			{"query_max_us", ts, int(l.maxLatency / time.Microsecond)},
			{"servfail", ts, l.servfail},
			{"tx", ts, l.outgoing},
			{"query_avg_us", ts, 0},
		}

		if l.nqueries > 0 {
			metrics[len(metrics)-1].Value =
				int((l.latency / time.Duration(l.nqueries)) / time.Microsecond)
		}
		l.stats = stats{queries: l.queries}

		// This is a precaution against leaking memory. We do not expect
		// that dnscache won't acknowledge finished queries, but there are
		// other ways to miss the notification (I/O errors, file rotation,
		// bugs in this program, ...)
		for k, v := range l.queries {
			if v.Before(ts) {
				delete(l.queries, k)
			}
		}
	}
	l.mu.Unlock()

	var dropCount int
	for _, m := range metrics {
		// NOTE(droyo) if we're buffering because of a bad connection, we always
		// want to drop the older metrics first, because that will give a more accurate
		// picture of *when* our connection became slow, and in general, newer metrics
		// are more valuable than older metrics overall, especially if you are using them
		// to generate alerts.
		select {
		case l.dst <- m:
		default:
			dropCount++
			<-l.dst
			if dropCount == 100 {
				verbosef("dropped %d metrics due to buffer full")
				dropCount = 0
			}
		}
	}
}

func toFile(filename string, out *template.Template, wg sync.WaitGroup) (chan metric, error) {
	c := make(chan metric, MetricsBufferMax)
	flags := os.O_WRONLY | os.O_APPEND | os.O_CREATE
	file, err := os.OpenFile(filename, flags, 0666)
	if err != nil {
		return nil, err
	}
	wg.Add(1)
	go func() {
		for m := range c {
			if err := out.Execute(file, m); err != nil {
				verbosef("to file: %v", err)
			}
		}
		file.Close()
		wg.Done()
	}()
	return c, nil
}

func toGraphite(hostport string, out *template.Template, wg sync.WaitGroup) (chan metric, error) {
	c := make(chan metric, MetricsBufferMax)
	if !strings.Contains(hostport, ":") {
		hostport += ":2003"
	}

	conn, err := net.Dial("tcp", hostport)
	if err != nil {
		return nil, err
	}

	wg.Add(1)
	go func() {
		bw := bufio.NewWriter(conn)
		for m := range c {
			out.Execute(bw, m)
		}
		bw.Flush()
		conn.Close()
		wg.Done()
	}()
	return c, nil
}

func main() {
	var src io.Reader
	var err error
	var c chan metric
	var wg sync.WaitGroup

	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	tmpl := template.Must(template.New("metric").Parse(
		"{{template \"name\" .}} {{.Value}} {{.Time.Unix}}\n"))
	_, err = tmpl.New("name").Parse(*nameTemplate)
	if err != nil {
		log.Fatal(err)
	}
	tmpl.Funcs(template.FuncMap{
		"lstrip": strings.TrimPrefix,
		"rstrip": strings.TrimSuffix,
		"join":   strings.Join,
		"split":  strings.Split,
	})

	Hostname, err = os.Hostname()
	Hostname = strings.SplitN(Hostname, ".", 2)[0]
	if err != nil {
		log.Fatal(err)
	}
	Service = guessServiceName()

	if len(*inputFile) > 0 {
		file, err := tailpipe.Open(*inputFile)
		if err != nil {
			log.Fatal(err)
		}
		src = file
		verbosef("following file %s", file.Name())
	} else {
		verbosef("reading from stdin")
		src = os.Stdin
	}
	dest := flag.Arg(0)

	if strings.HasPrefix(dest, "./") || strings.HasPrefix(dest, "/") {
		c, err = toFile(dest, tmpl, wg)
		verbosef("appending metrics to file %s", dest)
	} else {
		c, err = toGraphite(dest, tmpl, wg)
		verbosef("sending metrics to graphite server %s", dest)
	}

	if err != nil {
		log.Fatal(err)
	}

	// Duplicating the log on stdout makes this utility usable
	// as a daemontools log service that feeds multilog/s6-log.
	// For this reason we should not buffer writes to stdout
	rd, wr := io.Pipe()
	lr := &logReader{
		src:         rd,
		dst:         c,
		interval:    *interval,
		timestamped: *timestampedLog,
		done:        make(chan struct{}),
		mu:          new(sync.Mutex),
		stats: stats{
			queries: make(map[string]time.Time, 10000),
		},
	}
	go lr.run()
	_, err = io.Copy(io.MultiWriter(wr, os.Stdout), src)
	wr.Close()

	// Wait for metrics to finish getting to disk/graphite.
	// TODO(droyo) might want to have a deadline here.
	wg.Wait()
	if err != nil && err != io.EOF {
		verbosef("ending: %v", err)
		os.Exit(1)
	}
}
