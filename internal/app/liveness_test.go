package app

import (
	"errors"
	"testing"
)

var errBoom = errors.New("boom")

func TestIsSessionLive(t *testing.T) {
	orig := psLines
	defer func() { psLines = orig }()

	psLines = func() ([]string, error) {
		return []string{
			"claude --session-id 11111111-1111-4111-8111-111111111111 some prompt text",
			"claude --resume 22222222-2222-4222-8222-222222222222",
			"/bin/bash -c something",
		}, nil
	}

	if !isSessionLive("11111111-1111-4111-8111-111111111111") {
		t.Error("expected --session-id match to be live")
	}
	if !isSessionLive("22222222-2222-4222-8222-222222222222") {
		t.Error("expected --resume match to be live")
	}
	if isSessionLive("33333333-3333-4333-8333-333333333333") {
		t.Error("expected no match to be not live")
	}
	if isSessionLive("") {
		t.Error("expected empty session id to never be live")
	}
}

func TestIsSessionLive_PSErrorTreatedAsNotLive(t *testing.T) {
	orig := psLines
	defer func() { psLines = orig }()
	psLines = func() ([]string, error) { return nil, errBoom }
	if isSessionLive("anything") {
		t.Error("expected ps error to be treated as not-live, not panic or true")
	}
}
