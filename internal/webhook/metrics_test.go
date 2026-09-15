package webhook

import (
	"sync"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
)

func TestMetricsRecordRequest(t *testing.T) {
	m := NewMetrics()

	m.RecordRequest()
	m.RecordRequest()
	m.RecordRequest()

	stats := m.GetStats()
	if stats.TotalRequests != 3 {
		t.Errorf("Expected 3 total requests, got %d", stats.TotalRequests)
	}
}

func TestMetricsRecordSuccess(t *testing.T) {
	m := NewMetrics()

	m.RecordSuccess(analyzer.StatusTaskComplete, 100*time.Millisecond)
	m.RecordSuccess(analyzer.StatusQuestion, 200*time.Millisecond)

	stats := m.GetStats()
	if stats.SuccessfulRequests != 2 {
		t.Errorf("Expected 2 successful requests, got %d", stats.SuccessfulRequests)
	}

	// Check average latency
	expectedAvg := int64((100 + 200) / 2)
	if stats.AverageLatencyMs != expectedAvg {
		t.Errorf("Expected average latency %d ms, got %d ms", expectedAvg, stats.AverageLatencyMs)
	}
}

func TestMetricsRecordFailure(t *testing.T) {
	m := NewMetrics()

	m.RecordFailure()
	m.RecordFailure()
	m.RecordFailure()

	stats := m.GetStats()
	if stats.FailedRequests != 3 {
		t.Errorf("Expected 3 failed requests, got %d", stats.FailedRequests)
	}
}

func TestMetricsRecordRetry(t *testing.T) {
	m := NewMetrics()

	m.RecordRetry()
	m.RecordRetry()

	stats := m.GetStats()
	if stats.RetriedRequests != 2 {
		t.Errorf("Expected 2 retried requests, got %d", stats.RetriedRequests)
	}
}

func TestMetricsRecordRateLimited(t *testing.T) {
	m := NewMetrics()

	m.RecordRateLimited()

	stats := m.GetStats()
	if stats.RateLimitedRequests != 1 {
		t.Errorf("Expected 1 rate limited request, got %d", stats.RateLimitedRequests)
	}
}

func TestMetricsRecordCircuitOpen(t *testing.T) {
	m := NewMetrics()

	m.RecordCircuitOpen()
	m.RecordCircuitOpen()

	stats := m.GetStats()
	if stats.CircuitOpenRequests != 2 {
		t.Errorf("Expected 2 circuit open requests, got %d", stats.CircuitOpenRequests)
	}
}

func TestMetricsStatusCounters(t *testing.T) {
	m := NewMetrics()

	// Record successes for different statuses
	m.RecordSuccess(analyzer.StatusTaskComplete, 50*time.Millisecond)
	m.RecordSuccess(analyzer.StatusTaskComplete, 50*time.Millisecond)
	m.RecordSuccess(analyzer.StatusQuestion, 50*time.Millisecond)
	m.RecordSuccess(analyzer.StatusReviewComplete, 50*time.Millisecond)

	stats := m.GetStats()

	// Check individual status counts
	if stats.StatusCounts[analyzer.StatusTaskComplete] != 2 {
		t.Errorf("Expected 2 task complete, got %d", stats.StatusCounts[analyzer.StatusTaskComplete])
	}
	if stats.StatusCounts[analyzer.StatusQuestion] != 1 {
		t.Errorf("Expected 1 question, got %d", stats.StatusCounts[analyzer.StatusQuestion])
	}
	if stats.StatusCounts[analyzer.StatusReviewComplete] != 1 {
		t.Errorf("Expected 1 review complete, got %d", stats.StatusCounts[analyzer.StatusReviewComplete])
	}
}

func TestMetricsUpdateCircuitBreakerState(t *testing.T) {
	m := NewMetrics()

	m.UpdateCircuitBreakerState(StateOpen)

	stats := m.GetStats()
	if stats.CircuitBreakerState != StateOpen {
		t.Errorf("Expected StateOpen, got %v", stats.CircuitBreakerState)
	}

	m.UpdateCircuitBreakerState(StateClosed)
	stats = m.GetStats()
	if stats.CircuitBreakerState != StateClosed {
		t.Errorf("Expected StateClosed, got %v", stats.CircuitBreakerState)
	}
}

func TestMetricsAverageLatency(t *testing.T) {
	m := NewMetrics()

	// Record various latencies
	m.RecordSuccess(analyzer.StatusTaskComplete, 100*time.Millisecond)
	m.RecordSuccess(analyzer.StatusTaskComplete, 200*time.Millisecond)
	m.RecordSuccess(analyzer.StatusTaskComplete, 300*time.Millisecond)

	stats := m.GetStats()

	expectedAvg := int64((100 + 200 + 300) / 3)
	if stats.AverageLatencyMs != expectedAvg {
		t.Errorf("Expected average latency %d ms, got %d ms", expectedAvg, stats.AverageLatencyMs)
	}
}

func TestMetricsAverageLatencyNoRequests(t *testing.T) {
	m := NewMetrics()

	stats := m.GetStats()

	if stats.AverageLatencyMs != 0 {
		t.Errorf("Expected 0 average latency with no requests, got %d ms", stats.AverageLatencyMs)
	}
}

func TestMetricsSuccessRate(t *testing.T) {
	m := NewMetrics()

	// Record 7 successes and 3 failures
	for i := 0; i < 7; i++ {
		m.RecordRequest()
		m.RecordSuccess(analyzer.StatusTaskComplete, 50*time.Millisecond)
	}
	for i := 0; i < 3; i++ {
		m.RecordRequest()
		m.RecordFailure()
	}

	stats := m.GetStats()

	expectedRate := 70.0 // 7/10 * 100
	if stats.SuccessRate() != expectedRate {
		t.Errorf("Expected success rate %.1f%%, got %.1f%%", expectedRate, stats.SuccessRate())
	}
}

func TestMetricsFailureRate(t *testing.T) {
	m := NewMetrics()

	// Record 6 successes and 4 failures
	for i := 0; i < 6; i++ {
		m.RecordRequest()
		m.RecordSuccess(analyzer.StatusTaskComplete, 50*time.Millisecond)
	}
	for i := 0; i < 4; i++ {
		m.RecordRequest()
		m.RecordFailure()
	}

	stats := m.GetStats()

	expectedRate := 40.0 // 4/10 * 100
	if stats.FailureRate() != expectedRate {
		t.Errorf("Expected failure rate %.1f%%, got %.1f%%", expectedRate, stats.FailureRate())
	}
}

func TestMetricsSuccessRateNoRequests(t *testing.T) {
	m := NewMetrics()

	stats := m.GetStats()

	if stats.SuccessRate() != 0 {
		t.Errorf("Expected 0%% success rate with no requests, got %.1f%%", stats.SuccessRate())
	}
	if stats.FailureRate() != 0 {
		t.Errorf("Expected 0%% failure rate with no requests, got %.1f%%", stats.FailureRate())
	}
}

func TestMetricsReset(t *testing.T) {
	m := NewMetrics()

	// Record some data
	m.RecordRequest()
	m.RecordSuccess(analyzer.StatusTaskComplete, 100*time.Millisecond)
	m.RecordFailure()
	m.RecordRetry()
	m.RecordRateLimited()
	m.RecordCircuitOpen()
	m.UpdateCircuitBreakerState(StateOpen)

	// Reset
	m.Reset()

	stats := m.GetStats()

	// All counters should be zero
	if stats.TotalRequests != 0 {
		t.Errorf("Expected 0 total requests after reset, got %d", stats.TotalRequests)
	}
	if stats.SuccessfulRequests != 0 {
		t.Errorf("Expected 0 successful requests after reset, got %d", stats.SuccessfulRequests)
	}
	if stats.FailedRequests != 0 {
		t.Errorf("Expected 0 failed requests after reset, got %d", stats.FailedRequests)
	}
	if stats.RetriedRequests != 0 {
		t.Errorf("Expected 0 retried requests after reset, got %d", stats.RetriedRequests)
	}
	if stats.RateLimitedRequests != 0 {
		t.Errorf("Expected 0 rate limited requests after reset, got %d", stats.RateLimitedRequests)
	}
	if stats.CircuitOpenRequests != 0 {
		t.Errorf("Expected 0 circuit open requests after reset, got %d", stats.CircuitOpenRequests)
	}
	if stats.AverageLatencyMs != 0 {
		t.Errorf("Expected 0 average latency after reset, got %d", stats.AverageLatencyMs)
	}
	if stats.CircuitBreakerState != StateClosed {
		t.Errorf("Expected StateClosed after reset, got %v", stats.CircuitBreakerState)
	}
	if len(stats.StatusCounts) != 0 {
		t.Errorf("Expected empty status counts after reset, got %v", stats.StatusCounts)
	}
}

func TestMetricsConcurrency(t *testing.T) {
	m := NewMetrics()

	// Concurrent operations
	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrent requests
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			m.RecordRequest()
			if idx%2 == 0 {
				m.RecordSuccess(analyzer.StatusTaskComplete, 50*time.Millisecond)
			} else {
				m.RecordFailure()
			}
		}(i)
	}

	wg.Wait()

	stats := m.GetStats()

	// Should have recorded all requests
	if stats.TotalRequests != int64(numGoroutines) {
		t.Errorf("Expected %d total requests, got %d", numGoroutines, stats.TotalRequests)
	}

	// Should have roughly equal successes and failures
	if stats.SuccessfulRequests+stats.FailedRequests != int64(numGoroutines) {
		t.Errorf("Success + Failure should equal total: %d + %d != %d",
			stats.SuccessfulRequests, stats.FailedRequests, numGoroutines)
	}
}

func TestMetricsConcurrentStatusCounters(t *testing.T) {
	m := NewMetrics()

	var wg sync.WaitGroup
	statuses := []analyzer.Status{
		analyzer.StatusTaskComplete,
		analyzer.StatusQuestion,
		analyzer.StatusReviewComplete,
		analyzer.StatusPlanReady,
	}

	// Record 10 of each status concurrently
	for _, status := range statuses {
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(s analyzer.Status) {
				defer wg.Done()
				m.RecordSuccess(s, 50*time.Millisecond)
			}(status)
		}
	}

	wg.Wait()

	stats := m.GetStats()

	// Each status should have 10 counts
	for _, status := range statuses {
		count := stats.StatusCounts[status]
		if count != 10 {
			t.Errorf("Expected 10 for status %s, got %d", status, count)
		}
	}
}

func TestMetricsLargeLatencyValues(t *testing.T) {
	m := NewMetrics()

	// Record very large latencies
	m.RecordSuccess(analyzer.StatusTaskComplete, 10*time.Second)
	m.RecordSuccess(analyzer.StatusTaskComplete, 20*time.Second)

	stats := m.GetStats()

	expectedAvg := int64((10000 + 20000) / 2) // milliseconds
	if stats.AverageLatencyMs != expectedAvg {
		t.Errorf("Expected average latency %d ms, got %d ms", expectedAvg, stats.AverageLatencyMs)
	}
}
