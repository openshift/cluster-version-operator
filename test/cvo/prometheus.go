package cvo

import (
	"fmt"
	"regexp"
)

type prometheusTarget struct {
	Labels    map[string]string
	Health    string
	ScrapeUrl string
}

// Ref. https://github.com/openshift/origin/blob/f4d1c208855b7216452041276a7f909c3cf477ce/test/extended/prometheus/prometheus.go#L970
type prometheusTargets struct {
	Data struct {
		ActiveTargets []prometheusTarget
	}
	Status string
}

type labels map[string]string

func (t *prometheusTargets) Expect(l labels, health, scrapeURLPattern string) error {
	for _, target := range t.Data.ActiveTargets {
		match := true
		for k, v := range l {
			if target.Labels[k] != v {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if health != target.Health {
			continue
		}
		if !regexp.MustCompile(scrapeURLPattern).MatchString(target.ScrapeUrl) {
			continue
		}
		return nil
	}
	return fmt.Errorf("no match for %v with health %s and scrape URL %s", l, health, scrapeURLPattern)
}
