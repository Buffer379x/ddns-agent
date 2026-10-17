package sse

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStreamSurvivesServerDeadlineAndClosesOnRevocation(t *testing.T) {
	broker := NewBroker(1)
	var valid atomic.Bool
	valid.Store(true)
	srv := httptest.NewUnstartedServer(broker.Handler(valid.Load))
	srv.Config.WriteTimeout = 30 * time.Millisecond
	srv.Start()
	defer srv.Close()
	client := &http.Client{Timeout: time.Second}
	res, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	reader := bufio.NewReader(res.Body)
	if _, err = reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	reader.ReadString('\n')
	time.Sleep(60 * time.Millisecond)
	broker.BroadcastRecords()
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "records") {
		t.Fatalf("deadline killed stream: %q %v", line, err)
	}
	reader.ReadString('\n')
	reader.ReadString('\n')
	valid.Store(false)
	broker.BroadcastRecords()
	if _, err = reader.ReadString('\n'); err == nil {
		t.Fatal("revoked session received event")
	}
}
