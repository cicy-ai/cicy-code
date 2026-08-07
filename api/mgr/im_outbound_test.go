package main

import (
	"sync/atomic"
	"testing"
)

type countingIMTransport struct{ sends int32 }

func (f *countingIMTransport) Kind() string                          { return "test-im" }
func (f *countingIMTransport) Poll(string) ([]botMsg, string, error) { return nil, "", nil }
func (f *countingIMTransport) Send(botPeer, string) (string, error) {
	atomic.AddInt32(&f.sends, 1)
	return "m1", nil
}
func (f *countingIMTransport) Edit(botPeer, string, string) error { return errBotEditUnsupported }
func (f *countingIMTransport) Typing(botPeer) error               { return nil }
func (f *countingIMTransport) CanEdit() bool                      { return false }

func TestIMSendOutboundDeduplicatesReplyAcrossTransports(t *testing.T) {
	tr := &countingIMTransport{}
	msg := imOutboundMessage{AccountID: 87654321, Transport: tr, Peer: botPeer{ChatID: "chat-dedupe"}, Text: "same reply", Purpose: imOutboundPurposeReply}
	if _, err := imSendOutbound(msg); err != nil {
		t.Fatal(err)
	}
	if _, err := imSendOutbound(msg); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&tr.sends); got != 1 {
		t.Fatalf("transport sends = %d, want 1", got)
	}
}
