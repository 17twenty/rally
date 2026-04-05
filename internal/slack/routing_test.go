package slack

import (
	"reflect"
	"testing"
)

func TestParseMentions(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "single mention",
			text: "hey @Engineer-AE can you help?",
			want: []string{"Engineer"},
		},
		{
			name: "multiple mentions",
			text: "@CTO-AE and @Engineer-AE please review",
			want: []string{"CTO", "Engineer"},
		},
		{
			name: "no mentions",
			text: "hello team, any updates?",
			want: nil,
		},
		{
			name: "duplicate mention deduped",
			text: "@CEO-AE please approve, @CEO-AE",
			want: []string{"CEO"},
		},
		{
			name: "mixed case mention",
			text: "@cto-AE please advise",
			want: []string{"cto"},
		},
		{
			name: "mention without AE suffix ignored",
			text: "@Bob can you help?",
			want: nil,
		},
		{
			name: "app mention style",
			text: "<@U123> @Engineer-AE take a look",
			want: []string{"Engineer"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMentions(tt.text)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseMentions(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestChannelToRoles(t *testing.T) {
	tests := []struct {
		channel string
		want    []string
	}{
		{"engineering", []string{"Engineer", "CTO"}},
		{"ENGINEERING", []string{"Engineer", "CTO"}},
		{"product", []string{"Product", "CEO"}},
		{"general", []string{"CEO"}},
		{"exec", []string{"CEO", "CTO"}},
		{"random", []string{"CEO"}},
		{"sales", []string{"CEO"}},
		{"", []string{"CEO"}},
	}

	for _, tt := range tests {
		t.Run(tt.channel, func(t *testing.T) {
			got := ChannelToRoles(tt.channel)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ChannelToRoles(%q) = %v, want %v", tt.channel, got, tt.want)
			}
		})
	}
}
