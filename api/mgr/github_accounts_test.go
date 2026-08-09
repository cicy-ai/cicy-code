package main

import "testing"

func TestGithubTokenTail(t *testing.T) {
	if got := githubTokenTail("github_pat_abcdef1234"); got != "1234" {
		t.Fatalf("tail=%q", got)
	}
}

func TestGithubAccountNameValidation(t *testing.T) {
	for _, name := range []string{"w3c-ai", "cicy.team", "user_1"} {
		if !githubAccountNameRE.MatchString(name) {
			t.Fatalf("valid name rejected: %s", name)
		}
	}
	for _, name := range []string{"", "../secret", "bad name"} {
		if githubAccountNameRE.MatchString(name) {
			t.Fatalf("invalid name accepted: %s", name)
		}
	}
}
