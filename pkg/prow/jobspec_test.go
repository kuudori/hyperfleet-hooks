package prow

import (
	"testing"
)

const validJobSpec = `{
  "type": "presubmit",
  "job": "pull-ci-openshift-hyperfleet-hyperfleet-api-main-validate-commits",
  "refs": {
    "org": "openshift-hyperfleet",
    "repo": "hyperfleet-api",
    "base_ref": "main",
    "base_sha": "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
    "pulls": [
      {
        "number": 123,
        "author": "developer",
        "sha": "0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d3c2b1a",
        "title": "HYPERFLEET-123 - feat: add cluster validation"
      }
    ]
  }
}`

func TestParseJobSpec(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantJob string
		wantErr bool
	}{
		{
			name:    "valid: presubmit spec",
			raw:     validJobSpec,
			wantJob: "pull-ci-openshift-hyperfleet-hyperfleet-api-main-validate-commits",
			wantErr: false,
		},
		{
			name:    "valid: periodic spec without refs",
			raw:     `{"type":"periodic","job":"nightly"}`,
			wantJob: "nightly",
			wantErr: false,
		},
		{
			name:    "invalid: empty string",
			raw:     "",
			wantErr: true,
		},
		{
			name:    "invalid: malformed JSON",
			raw:     `{"type":"presubmit","refs":{`,
			wantErr: true,
		},
		{
			name:    "invalid: not an object",
			raw:     `"presubmit"`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := ParseJobSpec(tt.raw)

			if (err != nil) != tt.wantErr {
				t.Errorf("ParseJobSpec() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && spec.Job != tt.wantJob {
				t.Errorf("ParseJobSpec() Job = %q, want %q", spec.Job, tt.wantJob)
			}
		})
	}
}

func TestPullFromJobSpec(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantAuthor string
		wantTitle  string
		wantNumber int
		wantErr    bool
	}{
		{
			name:       "valid: single pull request",
			raw:        validJobSpec,
			wantNumber: 123,
			wantAuthor: "developer",
			wantTitle:  "HYPERFLEET-123 - feat: add cluster validation",
			wantErr:    false,
		},
		{
			name: "valid: batch job uses the first pull request",
			raw: `{"type":"batch","refs":{"org":"o","repo":"r","pulls":[` +
				`{"number":1,"author":"first","sha":"abc1234","title":"HYPERFLEET-1 - fix: first"},` +
				`{"number":2,"author":"second","sha":"def5678","title":"HYPERFLEET-2 - fix: second"}]}}`,
			wantNumber: 1,
			wantAuthor: "first",
			wantTitle:  "HYPERFLEET-1 - fix: first",
			wantErr:    false,
		},
		{
			name:    "invalid: malformed JSON",
			raw:     `{"refs":{"pulls":[}`,
			wantErr: true,
		},
		{
			name:    "invalid: empty string",
			raw:     "",
			wantErr: true,
		},
		{
			name:    "invalid: missing refs",
			raw:     `{"type":"periodic","job":"nightly"}`,
			wantErr: true,
		},
		{
			name:    "invalid: missing pulls",
			raw:     `{"type":"postsubmit","refs":{"org":"o","repo":"r","base_ref":"main"}}`,
			wantErr: true,
		},
		{
			name:    "invalid: empty pulls list",
			raw:     `{"type":"presubmit","refs":{"org":"o","repo":"r","pulls":[]}}`,
			wantErr: true,
		},
		{
			name:    "invalid: pull request without title",
			raw:     `{"type":"presubmit","refs":{"org":"o","repo":"r","pulls":[{"number":7,"author":"dev","sha":"abc1234"}]}}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pull, err := PullFromJobSpec(tt.raw)

			if (err != nil) != tt.wantErr {
				t.Errorf("PullFromJobSpec() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			if pull.Number != tt.wantNumber {
				t.Errorf("PullFromJobSpec() Number = %d, want %d", pull.Number, tt.wantNumber)
			}
			if pull.Author != tt.wantAuthor {
				t.Errorf("PullFromJobSpec() Author = %q, want %q", pull.Author, tt.wantAuthor)
			}
			if pull.Title != tt.wantTitle {
				t.Errorf("PullFromJobSpec() Title = %q, want %q", pull.Title, tt.wantTitle)
			}
		})
	}
}
