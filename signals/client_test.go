package signals

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQueryParsesVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("query"); got != "up" {
			t.Errorf("expr not forwarded: %q", got)
		}
		if got := r.URL.Query().Get("time"); got != "1788950000" {
			t.Errorf("time not forwarded: %q", got)
		}
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
		  {"metric":{"zone":"package"},"value":[1788950000,"12.5"]}]}}`))
	}))
	defer srv.Close()

	v, err := New(srv.URL).Query("up", time.Unix(1788950000, 0))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(v) != 1 || v[0].Value != 12.5 || v[0].Labels["zone"] != "package" {
		t.Fatalf("bad vector: %+v", v)
	}
}

func TestQueryRejectsPrometheusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`))
	}))
	defer srv.Close()

	if _, err := New(srv.URL).Query("((", time.Unix(1, 0)); err == nil {
		t.Fatal("a rejected expression must be an error, not an empty vector")
	}
}

func TestQueryRangeChunksLongWindows(t *testing.T) {
	type span struct{ start, end string }
	var got []span
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, span{r.URL.Query().Get("start"), r.URL.Query().Get("end")})
		w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer srv.Close()

	// 25,000 one-second steps: 0..24999 is three chunks of at most 10,000.
	if _, err := New(srv.URL).QueryRange("x", time.Unix(0, 0), time.Unix(24_999, 0), time.Second); err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	want := []span{{"0", "9999"}, {"10000", "19999"}, {"20000", "24999"}}
	if len(got) != len(want) {
		t.Fatalf("chunks = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chunk %d = %v, want %v (no gap, no overlap)", i, got[i], want[i])
		}
	}
}

func TestQueryRangeIndexesByTimestamp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
		  {"metric":{"zone":"package"},"values":[[100,"1"],[110,"2"]]}]}}`))
	}))
	defer srv.Close()

	m, err := New(srv.URL).QueryRange("x", time.Unix(100, 0), time.Unix(110, 0), 10*time.Second)
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(m) != 2 || m[100][0].Value != 1 || m[110][0].Value != 2 {
		t.Fatalf("bad matrix: %+v", m)
	}
}
