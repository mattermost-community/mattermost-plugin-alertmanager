package main

import (
	"errors"
	"testing"
	"time"
)

func TestQueryGroupsRunsConcurrentlyAndKeepsOrder(t *testing.T) {
	groups := []amGroup{{URL: "a"}, {URL: "b"}, {URL: "c"}}
	start := time.Now()
	vals, errs := queryGroups(groups, func(g amGroup) (string, error) {
		time.Sleep(200 * time.Millisecond)
		if g.URL == "b" {
			return "", errors.New("down")
		}
		return g.URL, nil
	})
	if elapsed := time.Since(start); elapsed > 450*time.Millisecond {
		t.Fatalf("three 200ms calls took %s; they ran one at a time", elapsed)
	}
	if vals[0] != "a" || vals[2] != "c" || errs[1] == nil || errs[0] != nil {
		t.Fatalf("results out of order: %v %v", vals, errs)
	}
}
