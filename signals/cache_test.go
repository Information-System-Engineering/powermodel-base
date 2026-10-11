package signals

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheFetchesOncePerExpression(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[
		  {"metric":{},"values":[[100,"1"],[110,"2"],[120,"3"]]}]}}`)
	}))
	defer srv.Close()

	p := NewCache(New(srv.URL), time.Unix(100, 0), time.Unix(120, 0), 10*time.Second)
	for _, want := range []struct {
		ts int64
		v  float64
	}{{100, 1}, {110, 2}, {120, 3}, {100, 1}} {
		got, err := p.Query("x", time.Unix(want.ts, 0))
		if err != nil {
			t.Fatalf("Query at %d: %v", want.ts, err)
		}
		if len(got) != 1 || got[0].Value != want.v {
			t.Fatalf("at %d: got %+v, want %v", want.ts, got, want.v)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("expected one query_range for one expression, got %d", n)
	}
}

func TestCacheReturnsEmptyForAStepWithNoData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[
		  {"metric":{},"values":[[100,"1"]]}]}}`)
	}))
	defer srv.Close()

	p := NewCache(New(srv.URL), time.Unix(100, 0), time.Unix(120, 0), 10*time.Second)
	got, err := p.Query("x", time.Unix(110, 0))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a gap must be an empty vector, not an error or a stale value: %+v", got)
	}
}
