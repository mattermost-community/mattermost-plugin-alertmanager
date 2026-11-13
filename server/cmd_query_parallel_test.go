package main

import (
	"errors"
	"testing"
	"time"
)

func TestQueryGroupsRunsConcurrentlyAndKeepsOrder(t *testing.T) {
	groups := []amGroup{{URL: "a"}, {URL: "b"}, {URL: "c"}, {URL: "d"}}
	start := time.Now()
	vals, errs := queryGroups(groups, func(g amGroup) (string, error) {
		time.Sleep(250 * time.Millisecond)
		if g.URL == "b" {
			return "", errors.New("down")
		}
		return g.URL, nil
	})
	if elapsed := time.Since(start); elapsed > 700*time.Millisecond {
		t.Fatalf("four 250ms calls took %s; they ran one at a time", elapsed)
	}
	if vals[0] != "a" || vals[2] != "c" || vals[3] != "d" || errs[1] == nil || errs[0] != nil {
		t.Fatalf("results out of order: %v %v", vals, errs)
	}
}
