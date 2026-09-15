package webhook

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
)

// Metrics tracks webhook statistics
type Metrics struct {
	// Request counters
	totalRequests       atomic.Int64
	successfulRequests  atomic.Int64
	failedRequests      atomic.Int64
	retriedRequests     atomic.Int64
	rateLimitedRequests atomic.Int64
	circuitOpenRequests atomic.Int64

	// Status-based counters
	statusCounters map[analyzer.Status]*atomic.Int64
	mu             sync.RWMutex

	// Latency tracking
	totalLatency atomic.Int64 // in milliseconds
	requestCount atomic.Int64 // for average calculation

	// Circuit breaker state
	circuitBreakerState atomic.Int32 // 0=closed, 1=open, 2=half-open
}

// NewMetrics creates a new metrics tracker
func NewMetrics() *Metrics {
	return &Metrics{
		statusCounters: make(map[analyzer.Status]*atomic.Int64),
	}
}

// RecordRequest records a webhook request attempt
func (m *Metrics) RecordRequest() {
	m.totalRequests.Add(1)
}

// RecordSuccess records a successful webhook delivery
func (m *Metrics) RecordSuccess(status analyzer.Status, latency time.Duration) {
	m.successfulRequests.Add(1)
	m.recordLatency(latency)
	m.incrementStatusCounter(status)
}

// RecordFailure records a failed webhook delivery
func (m *Metrics) RecordFailure() {
	m.failedRequests.Add(1)
}

// RecordRetry records a retry attempt
func (m *Metrics) RecordRetry() {
	m.retriedRequests.Add(1)
}

// RecordRateLimited records a rate-limited request
func (m *Metrics) RecordRateLimited() {
	m.rateLimitedRequests.Add(1)
}

// RecordCircuitOpen records a request blocked by circuit breaker
func (m *Metrics) RecordCircuitOpen() {
	m.circuitOpenRequests.Add(1)
}

// recordLatency records request latency
func (m *Metrics) recordLatency(latency time.Duration) {
	m.totalLatency.Add(latency.Milliseconds())
	m.requestCount.Add(1)
}

// incrementStatusCounter increments counter for a specific status
func (m *Metrics) incrementStatusCounter(status analyzer.Status) {
	m.mu.Lock()
	counter, exists := m.statusCounters[status]
	if !exists {
		counter = &atomic.Int64{}
		m.statusCounters[status] = counter
	}
	m.mu.Unlock()

	counter.Add(1)
}

// UpdateCircuitBreakerState updates the circuit breaker state
func (m *Metrics) UpdateCircuitBreakerState(state CircuitBreakerState) {
	m.circuitBreakerState.Store(int32(state))
}

// GetStats returns current statistics
func (m *Metrics) GetStats() Stats {
	m.mu.RLock()
	statusCounts := make(map[analyzer.Status]int64)
	for status, counter := range m.statusCounters {
		statusCounts[status] = counter.Load()
	}
	m.mu.RUnlock()

	requestCount := m.requestCount.Load()
	avgLatency := int64(0)
	if requestCount > 0 {
		avgLatency = m.totalLatency.Load() / requestCount
	}

	return Stats{
		TotalRequests:       m.totalRequests.Load(),
		SuccessfulRequests:  m.successfulRequests.Load(),
		FailedRequests:      m.failedRequests.Load(),
		RetriedRequests:     m.retriedRequests.Load(),
		RateLimitedRequests: m.rateLimitedRequests.Load(),
		CircuitOpenRequests: m.circuitOpenRequests.Load(),
		StatusCounts:        statusCounts,
		AverageLatencyMs:    avgLatency,
		CircuitBreakerState: CircuitBreakerState(m.circuitBreakerState.Load()),
	}
}

// Reset resets all metrics (useful for testing)
func (m *Metrics) Reset() {
	m.totalRequests.Store(0)
	m.successfulRequests.Store(0)
	m.failedRequests.Store(0)
	m.retriedRequests.Store(0)
	m.rateLimitedRequests.Store(0)
	m.circuitOpenRequests.Store(0)
	m.totalLatency.Store(0)
	m.requestCount.Store(0)
	m.circuitBreakerState.Store(0)

	m.mu.Lock()
	m.statusCounters = make(map[analyzer.Status]*atomic.Int64)
	m.mu.Unlock()
}

// Stats represents a snapshot of metrics
type Stats struct {
	TotalRequests       int64
	SuccessfulRequests  int64
	FailedRequests      int64
	RetriedRequests     int64
	RateLimitedRequests int64
	CircuitOpenRequests int64
	StatusCounts        map[analyzer.Status]int64
	AverageLatencyMs    int64
	CircuitBreakerState CircuitBreakerState
}

// SuccessRate returns the success rate as a percentage
func (s *Stats) SuccessRate() float64 {
	if s.TotalRequests == 0 {
		return 0
	}
	return float64(s.SuccessfulRequests) / float64(s.TotalRequests) * 100
}

// FailureRate returns the failure rate as a percentage
func (s *Stats) FailureRate() float64 {
	if s.TotalRequests == 0 {
		return 0
	}
	return float64(s.FailedRequests) / float64(s.TotalRequests) * 100
}
