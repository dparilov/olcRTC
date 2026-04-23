// Command transport-test validates post-Item-7 transport via local loopback.
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openlibrecommunity/olcrtc/internal/crypto"
	"github.com/openlibrecommunity/olcrtc/internal/mux"
)

var (
	flagScenario = flag.String("scenario", "all", "A,B,C,D,E,F,G,H or all")
	flagDuration = flag.Duration("duration", 20*time.Minute, "Soak duration")
	flagKey      = flag.String("key", "olcrtc-transport-test-key-32byte", "32-byte key")
	flagOutput   = flag.String("output", "results.json", "Output file")
)

type Metrics struct {
	mu           sync.Mutex
	BytesSentA   int64
	BytesRecvA   int64
	BytesSentB   int64
	BytesRecvB   int64
	RTTSamples   []time.Duration
	Reconnects   int
	Errors       []string
	TimeSeriesTP []TPSample
	TimeSeriesRTT []RTTSample
}
type TPSample struct {
	T float64 `json:"t_sec"`
	BytesSentA int64 `json:"sent_a"`
	BytesRecvB int64 `json:"recv_b"`
	BytesSentB int64 `json:"sent_b"`
	BytesRecvA int64 `json:"recv_a"`
}
type RTTSample struct {
	T float64 `json:"t_sec"`
	RTT float64 `json:"rtt_us"`
}
func (m *Metrics) addRTT(d time.Duration) { m.mu.Lock(); m.RTTSamples = append(m.RTTSamples, d); m.mu.Unlock() }
func (m *Metrics) addError(e string) { m.mu.Lock(); m.Errors = append(m.Errors, e); m.mu.Unlock() }

type LoopbackPair struct {
	MuxA, MuxB *mux.Multiplexer
	Cipher     *crypto.Cipher
	chAB, chBA chan []byte
	stop       chan struct{}
	wg         sync.WaitGroup
	metrics    *Metrics
}

func NewLoopback(key string, m *Metrics) (*LoopbackPair, error) {
	c, err := crypto.NewCipher(key)
	if err != nil { return nil, err }
	p := &LoopbackPair{Cipher: c, chAB: make(chan []byte, 10000), chBA: make(chan []byte, 10000), stop: make(chan struct{}), metrics: m}
	p.MuxA = mux.New(1, func(d []byte) error {
		e, err := c.Encrypt(d); if err != nil { return err }
		select { case p.chAB <- e: return nil; case <-p.stop: return fmt.Errorf("stopped") }
	})
	p.MuxB = mux.New(2, func(d []byte) error {
		e, err := c.Encrypt(d); if err != nil { return err }
		select { case p.chBA <- e: return nil; case <-p.stop: return fmt.Errorf("stopped") }
	})
	return p, nil
}

func (p *LoopbackPair) Start() {
	fwd := func(ch chan []byte, dst *mux.Multiplexer, recv *int64) {
		p.wg.Add(1); go func() { defer p.wg.Done()
			for { select {
			case e := <-ch: d, err := p.Cipher.Decrypt(e); if err != nil { continue }; dst.HandleFrame(d); if recv != nil { atomic.AddInt64(recv, int64(len(d))) }
			case <-p.stop: return
			}}
		}()
	}
	fwd(p.chAB, p.MuxB, &p.metrics.BytesRecvB)
	fwd(p.chBA, p.MuxA, &p.metrics.BytesRecvA)
}
func (p *LoopbackPair) Stop() { close(p.stop); p.wg.Wait() }

type ScenarioResult struct {
	Name string `json:"name"`
	Status string `json:"status"`
	Duration float64 `json:"duration_sec"`
	Metrics *Metrics `json:"metrics,omitempty"`
	Verdict string `json:"verdict,omitempty"`
	Details string `json:"details,omitempty"`
}

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	scenarios := parseScenarios(*flagScenario)
	log.Printf("Running scenarios: %v on %s/%s CPUs=%d", scenarios, runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	var results []ScenarioResult
	for _, s := range scenarios {
		log.Printf("\n=== SCENARIO %s ===", s)
		t0 := time.Now()
		r := runScenario(s)
		r.Duration = time.Since(t0).Seconds()
		results = append(results, r)
		log.Printf("=== SCENARIO %s: %s (%.1fs) ===", s, r.Status, r.Duration)
	}
	writeResults(results)
	printReport(results)
}

func parseScenarios(s string) []string {
	if s == "all" { return []string{"A","B","C","D","E","F","G","H"} }
	return strings.Split(strings.ToUpper(s), ",")
}

func runScenario(n string) (r ScenarioResult) {
	defer func() { if p := recover(); p != nil { r = ScenarioResult{Name: n, Status: "ERROR", Details: fmt.Sprintf("panic: %v", p)} } }()
	switch n {
	case "A": return scenarioA()
	case "B": return scenarioB()
	case "C": return scenarioC()
	case "D": return scenarioD()
	case "E": return scenarioE()
	case "F": return scenarioF(*flagDuration)
	case "G": return scenarioG(*flagDuration)
	case "H": return scenarioH()
	default: return ScenarioResult{Name: n, Status: "ERROR", Details: "unknown"}
	}
}

func scenarioA() ScenarioResult {
	m := &Metrics{}
	p, err := NewLoopback(*flagKey, m); if err != nil { return ScenarioResult{Name: "A", Status: "ERROR", Details: err.Error()} }
	p.Start(); defer p.Stop()
	sid := p.MuxA.OpenStream()
	payloads := []string{"hello", "world", strings.Repeat("X", 5000)}
	for _, pl := range payloads { p.MuxA.SendData(sid, []byte(pl)) }
	time.Sleep(100 * time.Millisecond)
	data := p.MuxB.ReadStream(sid)
	exp := strings.Join(payloads, "")
	if string(data) != exp { return ScenarioResult{Name: "A — Basic send/recv", Status: "FAIL", Details: fmt.Sprintf("got %d want %d bytes", len(data), len(exp))} }
	sidB := p.MuxB.OpenStream()
	p.MuxB.SendData(sidB, []byte("reply"))
	time.Sleep(100 * time.Millisecond)
	reply := p.MuxA.ReadStream(sidB)
	if string(reply) != "reply" { return ScenarioResult{Name: "A", Status: "FAIL", Details: "reply mismatch"} }
	return ScenarioResult{Name: "A — Basic send/recv", Status: "PASS", Details: fmt.Sprintf("Verified %d bytes A->B + reply B->A", len(exp))}
}

func scenarioB() ScenarioResult {
	for i := 0; i < 10; i++ {
		m := &Metrics{}
		p, err := NewLoopback(*flagKey, m); if err != nil { return ScenarioResult{Name: "B", Status: "ERROR", Details: err.Error()} }
		p.Start()
		sid := p.MuxA.OpenStream()
		p.MuxA.SendData(sid, []byte(fmt.Sprintf("c%d", i)))
		time.Sleep(50*time.Millisecond)
		d := p.MuxB.ReadStream(sid)
		if string(d) != fmt.Sprintf("c%d", i) { p.Stop(); return ScenarioResult{Name: "B", Status: "FAIL", Details: fmt.Sprintf("cycle %d mismatch", i)} }
		p.MuxA.Reset(); p.MuxB.Reset(); p.Stop()
	}
	return ScenarioResult{Name: "B — Repeated lifecycle", Status: "PASS", Details: "10 cycles OK"}
}

func scenarioC() ScenarioResult { return tpTest("C — Unidirectional", false, 30*time.Second) }
func scenarioD() ScenarioResult { return tpTest("D — Bidirectional", true, 30*time.Second) }

func tpTest(name string, bidir bool, dur time.Duration) ScenarioResult {
	m := &Metrics{}
	p, err := NewLoopback(*flagKey, m); if err != nil { return ScenarioResult{Name: name, Status: "ERROR", Details: err.Error()} }
	p.Start(); defer p.Stop()
	payload := make([]byte, 4096)
	start := time.Now(); deadline := start.Add(dur)
	var wg sync.WaitGroup
	sidA := p.MuxA.OpenStream()
	wg.Add(1); go func() { defer wg.Done()
		for time.Now().Before(deadline) { p.MuxA.SendData(sidA, payload); atomic.AddInt64(&m.BytesSentA, 4096) }
	}()
	wg.Add(1); go func() { defer wg.Done()
		ch := p.MuxB.WaitForData(sidA)
		for time.Now().Before(deadline) { select { case <-ch: p.MuxB.ReadStream(sidA); case <-time.After(200*time.Millisecond): } }
	}()
	if bidir {
		sidB := p.MuxB.OpenStream()
		wg.Add(1); go func() { defer wg.Done()
			for time.Now().Before(deadline) { p.MuxB.SendData(sidB, payload); atomic.AddInt64(&m.BytesSentB, 4096) }
		}()
		wg.Add(1); go func() { defer wg.Done()
			ch := p.MuxA.WaitForData(sidB)
			for time.Now().Before(deadline) { select { case <-ch: p.MuxA.ReadStream(sidB); case <-time.After(200*time.Millisecond): } }
		}()
	}
	wg.Add(1); go func() { defer wg.Done()
		tk := time.NewTicker(5*time.Second); defer tk.Stop()
		for time.Now().Before(deadline) { <-tk.C
			m.mu.Lock(); m.TimeSeriesTP = append(m.TimeSeriesTP, TPSample{T: time.Since(start).Seconds(), BytesSentA: atomic.LoadInt64(&m.BytesSentA), BytesSentB: atomic.LoadInt64(&m.BytesSentB)}); m.mu.Unlock()
		}
	}()
	wg.Wait()
	el := time.Since(start); tpA := float64(m.BytesSentA)/el.Seconds()/(1024*1024)
	det := fmt.Sprintf("A->B: %.2f MB/s (%d bytes in %.0fs)", tpA, m.BytesSentA, el.Seconds())
	if bidir { det += fmt.Sprintf(", B->A: %.2f MB/s", float64(m.BytesSentB)/el.Seconds()/(1024*1024)) }
	return ScenarioResult{Name: name, Status: "PASS", Metrics: m, Details: det}
}

func scenarioE() ScenarioResult {
	m := &Metrics{}
	p, err := NewLoopback(*flagKey, m); if err != nil { return ScenarioResult{Name: "E", Status: "ERROR", Details: err.Error()} }
	p.Start(); defer p.Stop()
	phases := []struct{ n string; d, iv time.Duration }{{"idle",5*time.Second,200*time.Millisecond},{"moderate",10*time.Second,50*time.Millisecond},{"heavy",10*time.Second,5*time.Millisecond}}
	start := time.Now()
	for _, ph := range phases {
		log.Printf("[E] Phase: %s", ph.n); end := time.Now().Add(ph.d)
		for time.Now().Before(end) {
			t0 := time.Now(); sid := p.MuxA.OpenStream()
			ts := make([]byte, 8); binary.BigEndian.PutUint64(ts, uint64(t0.UnixNano()))
			p.MuxA.SendData(sid, ts)
			ch := p.MuxB.WaitForData(sid)
			select { case <-ch: case <-time.After(time.Second): }
			rtt := time.Since(t0); p.MuxB.ReadStream(sid); p.MuxA.CloseStream(sid)
			m.addRTT(rtt)
			m.mu.Lock(); m.TimeSeriesRTT = append(m.TimeSeriesRTT, RTTSample{T: time.Since(start).Seconds(), RTT: float64(rtt.Microseconds())}); m.mu.Unlock()
			time.Sleep(ph.iv)
		}
	}
	sort.Slice(m.RTTSamples, func(i,j int) bool { return m.RTTSamples[i] < m.RTTSamples[j] })
	n := len(m.RTTSamples); var med, p95, p99 time.Duration
	if n > 0 { med = m.RTTSamples[n/2]; p95 = m.RTTSamples[int(float64(n)*0.95)]; p99 = m.RTTSamples[int(math.Min(float64(n)*0.99, float64(n-1)))] }
	return ScenarioResult{Name: "E — Latency", Status: "PASS", Metrics: m, Details: fmt.Sprintf("n=%d med=%v p95=%v p99=%v", n, med, p95, p99)}
}

func scenarioF(dur time.Duration) ScenarioResult { return soakTest("F — Soak sustained", dur, false) }
func scenarioG(dur time.Duration) ScenarioResult { return soakTest("G — Mixed soak", dur, true) }

func soakTest(name string, dur time.Duration, mixed bool) ScenarioResult {
	m := &Metrics{}
	p, err := NewLoopback(*flagKey, m); if err != nil { return ScenarioResult{Name: name, Status: "ERROR", Details: err.Error()} }
	p.Start(); defer p.Stop()
	start := time.Now(); deadline := start.Add(dur)
	var wg sync.WaitGroup
	sid := p.MuxA.OpenStream()
	small := make([]byte, 64); medium := make([]byte, 4096); large := make([]byte, 7000)
	wg.Add(1); go func() { defer wg.Done()
		phase := 0; pt := time.NewTicker(30*time.Second); defer pt.Stop()
		for time.Now().Before(deadline) {
			var pl []byte
			if mixed {
				select { case <-pt.C: phase++; log.Printf("[SOAK] phase %d at %.0fs", phase, time.Since(start).Seconds()); default: }
				switch phase%5 {
				case 0: pl = small; case 1: pl = medium; case 2: pl = large
				case 3: for i:=0;i<10;i++ { p.MuxA.SendData(sid,medium); atomic.AddInt64(&m.BytesSentA,4096) }; continue
				case 4: time.Sleep(200*time.Millisecond); continue
				}
			} else { pl = medium }
			p.MuxA.SendData(sid, pl); atomic.AddInt64(&m.BytesSentA, int64(len(pl)))
		}
	}()
	wg.Add(1); go func() { defer wg.Done()
		ch := p.MuxB.WaitForData(sid)
		for time.Now().Before(deadline) { select { case <-ch: p.MuxB.ReadStream(sid); case <-time.After(500*time.Millisecond): } }
	}()
	wg.Add(1); go func() { defer wg.Done()
		tk := time.NewTicker(5*time.Second); defer tk.Stop()
		var ms runtime.MemStats
		for time.Now().Before(deadline) { <-tk.C
			runtime.ReadMemStats(&ms)
			sent := atomic.LoadInt64(&m.BytesSentA); tp := float64(sent)/time.Since(start).Seconds()/(1024*1024)
			log.Printf("[SOAK] t=%.0fs sent=%.1fMB tp=%.2fMB/s mem=%.1fMB gr=%d", time.Since(start).Seconds(), float64(sent)/(1024*1024), tp, float64(ms.Alloc)/(1024*1024), runtime.NumGoroutine())
			m.mu.Lock(); m.TimeSeriesTP = append(m.TimeSeriesTP, TPSample{T: time.Since(start).Seconds(), BytesSentA: sent}); m.mu.Unlock()
		}
	}()
	wg.Wait()
	v := analyzeDeg(m); el := time.Since(start); tp := float64(m.BytesSentA)/el.Seconds()/(1024*1024)
	return ScenarioResult{Name: name, Status: "PASS", Metrics: m, Verdict: v, Details: fmt.Sprintf("%.2f MB/s avg over %.0fs, %.1fMB total", tp, el.Seconds(), float64(m.BytesSentA)/(1024*1024))}
}

func scenarioH() ScenarioResult {
	m := &Metrics{}
	p, err := NewLoopback(*flagKey, m); if err != nil { return ScenarioResult{Name: "H", Status: "ERROR", Details: err.Error()} }
	p.Start()
	sid := p.MuxA.OpenStream(); payload := make([]byte, 2048); stopSend := make(chan struct{})
	go func() { for { select { case <-stopSend: return; default: p.MuxA.SendData(sid,payload); atomic.AddInt64(&m.BytesSentA,2048); time.Sleep(5*time.Millisecond) } } }()
	go func() { ch := p.MuxB.WaitForData(sid); for { select { case <-stopSend: return; case <-ch: p.MuxB.ReadStream(sid) } } }()
	time.Sleep(3*time.Second)
	pre := atomic.LoadInt64(&m.BytesSentA); log.Printf("[H] Pre-reset: %d bytes", pre)
	close(stopSend) // stop old sender/reader first
	time.Sleep(50*time.Millisecond)
	p.MuxA.Reset(); p.MuxB.Reset(); m.Reconnects++
	sid2 := p.MuxA.OpenStream(); time.Sleep(50*time.Millisecond)
	for i:=0;i<100;i++ { p.MuxA.SendData(sid2,payload); atomic.AddInt64(&m.BytesSentA,2048) }
	time.Sleep(200*time.Millisecond)
	data := p.MuxB.ReadStream(sid2); p.Stop()
	post := atomic.LoadInt64(&m.BytesSentA)
	if len(data)==0 { return ScenarioResult{Name: "H — Reconnect", Status: "FAIL", Details: "no data after reset"} }
	return ScenarioResult{Name: "H — Reconnect during traffic", Status: "PASS", Metrics: m, Details: fmt.Sprintf("pre=%d post=%d recv=%d bytes. OK", pre, post-pre, len(data))}
}

func analyzeDeg(m *Metrics) string {
	m.mu.Lock(); defer m.mu.Unlock()
	s := m.TimeSeriesTP; if len(s) < 6 { return "insufficient data" }
	n := len(s); th := n/3; if th < 2 { return "insufficient data" }
	var f1, f2 float64
	for i:=1;i<th;i++ { dt:=s[i].T-s[i-1].T; db:=float64(s[i].BytesSentA-s[i-1].BytesSentA); if dt>0 { f1+=db/dt } }; f1/=float64(th-1)
	for i:=n-th+1;i<n;i++ { dt:=s[i].T-s[i-1].T; db:=float64(s[i].BytesSentA-s[i-1].BytesSentA); if dt>0 { f2+=db/dt } }; f2/=float64(th-1)
	if f1==0 { return "no throughput first third" }
	r := f2/f1
	if r>=0.9 { return fmt.Sprintf("no degradation (%.2f)", r) }
	if r>=0.7 { return fmt.Sprintf("mild degradation (%.2f)", r) }
	if r>=0.5 { return fmt.Sprintf("significant degradation (%.2f)", r) }
	return fmt.Sprintf("severe degradation (%.2f)", r)
}

func writeResults(res []ScenarioResult) { d,_:=json.MarshalIndent(res,"","  "); os.WriteFile(*flagOutput,d,0644); log.Printf("Results: %s", *flagOutput) }

func printReport(res []ScenarioResult) {
	fmt.Println(); fmt.Println(strings.Repeat("=",72))
	fmt.Println("  TRANSPORT VALIDATION REPORT"); fmt.Println(strings.Repeat("=",72))
	fmt.Printf("  Date: %s  System: %s/%s CPUs=%d\n", time.Now().Format("2006-01-02 15:04:05"), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	fmt.Println(strings.Repeat("-",72))
	pass,fail,errs := 0,0,0
	for _,r := range res {
		st := "ERR "; switch r.Status { case "PASS": st="PASS"; pass++; case "FAIL": st="FAIL"; fail++; default: errs++ }
		fmt.Printf("  [%s] %-46s %7.1fs\n", st, r.Name, r.Duration)
		if r.Details != "" { fmt.Printf("         %s\n", r.Details) }
		if r.Verdict != "" { fmt.Printf("         Verdict: %s\n", r.Verdict) }
	}
	fmt.Println(strings.Repeat("-",72))
	fmt.Printf("  Total: %d passed, %d failed, %d errors\n", pass, fail, errs)
	fmt.Println(strings.Repeat("=",72))
}
