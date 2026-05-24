/*
Copyright 2026.
*/

package classifier_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
)

func TestConfidence(t *testing.T) {
	cases := []struct {
		name   string
		points int
		want   string
	}{
		{"well above high threshold", 500, classifier.ConfidenceHigh},
		{"exactly at high threshold", 240, classifier.ConfidenceHigh},
		{"one below high threshold", 239, classifier.ConfidenceMedium},
		{"exactly at min threshold", 70, classifier.ConfidenceMedium},
		{"one below min threshold", 69, ""},
		{"zero points", 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifier.Confidence(tc.points, 240, 70)
			assert.Equal(t, tc.want, got)
		})
	}
}
