package cvo

import (
	"strings"
	"testing"

	imagev1 "github.com/openshift/api/image/v1"
	corev1 "k8s.io/api/core/v1"
)

func Test_newManifestOverrideMapper(t *testing.T) {
	type args struct {
		override string
		imageRef *imagev1.ImageStream
	}
	imageRef1 := &imagev1.ImageStream{
		Spec: imagev1.ImageStreamSpec{
			Tags: []imagev1.TagReference{
				{Name: "image1", From: &corev1.ObjectReference{Kind: "DockerImage", Name: "reg/repo@sha256:e7b801fc86f79eefd63b881feb1d2a6dc00e7d79bbd09765a2c3efb3996a1289"}},
			},
		},
	}

	tests := []struct {
		name    string
		args    args
		input   string
		output  string
		wantErr string
	}{
		{
			name: "replace at beginning of file",
			args: args{
				override: "reg2/repo2",
				imageRef: imageRef1,
			},
			input:  "reg/repo@sha256:e7b801fc86f79eefd63b881feb1d2a6dc00e7d79bbd09765a2c3efb3996a1289\n",
			output: "reg2/repo2@sha256:e7b801fc86f79eefd63b881feb1d2a6dc00e7d79bbd09765a2c3efb3996a1289\n",
		},
		{
			name: "replace at end of file",
			args: args{
				override: "reg2/repo2",
				imageRef: imageRef1,
			},
			input:  "image: reg/repo@sha256:e7b801fc86f79eefd63b881feb1d2a6dc00e7d79bbd09765a2c3efb3996a1289\n",
			output: "image: reg2/repo2@sha256:e7b801fc86f79eefd63b881feb1d2a6dc00e7d79bbd09765a2c3efb3996a1289\n",
		},
		{
			name: "replace in quotes",
			args: args{
				override: "reg2/repo2",
				imageRef: imageRef1,
			},
			input:  "image: \"reg/repo@sha256:e7b801fc86f79eefd63b881feb1d2a6dc00e7d79bbd09765a2c3efb3996a1289\"",
			output: "image: \"reg2/repo2@sha256:e7b801fc86f79eefd63b881feb1d2a6dc00e7d79bbd09765a2c3efb3996a1289\"",
		},
		{
			name: "skip - require kind DockerImage",
			args: args{
				override: "reg2/repo2",
				imageRef: &imagev1.ImageStream{
					Spec: imagev1.ImageStreamSpec{
						Tags: []imagev1.TagReference{
							{Name: "image1", From: &corev1.ObjectReference{Kind: "Other", Name: "reg/repo@sha256:e7b801fc86f79eefd63b881feb1d2a6dc00e7d79bbd09765a2c3efb3996a1289"}},
						},
					},
				},
			},
			input:  "image: \"reg/repo@sha256:e7b801fc86f79eefd63b881feb1d2a6dc00e7d79bbd09765a2c3efb3996a1289\"",
			output: "image: \"reg/repo@sha256:e7b801fc86f79eefd63b881feb1d2a6dc00e7d79bbd09765a2c3efb3996a1289\"",
		},
		{
			name: "error - ref is not parseable",
			args: args{
				override: "reg2/repo2",
				imageRef: &imagev1.ImageStream{
					Spec: imagev1.ImageStreamSpec{
						Tags: []imagev1.TagReference{
							{Name: "image1", From: &corev1.ObjectReference{Kind: "DockerImage", Name: "reg/repo@sha256:1234"}},
						},
					},
				},
			},
			input:   "image: \"reg/repo@sha256:e7b801fc86f79eefd63b881feb1d2a6dc00e7d79bbd09765a2c3efb3996a1289\"",
			wantErr: "unable to parse image reference for tag \"image1\" from payload: invalid reference format",
		},
		{
			name: "error - override has tag",
			args: args{
				override: "reg2/repo2:tag",
				imageRef: imageRef1,
			},
			input:   "image: \"reg/repo@sha256:e7b801fc86f79eefd63b881feb1d2a6dc00e7d79bbd09765a2c3efb3996a1289\"",
			wantErr: "the payload repository may not have a tag or digest specified",
		},
		{
			name: "error - override has valid digest",
			args: args{
				override: "reg2/repo2@sha256:e7b801fc86f79eefd63b881feb1d2a6dc00e7d79bbd09765a2c3efb3996a1289",
				imageRef: imageRef1,
			},
			input:   "image: \"reg/repo@sha256:e7b801fc86f79eefd63b881feb1d2a6dc00e7d79bbd09765a2c3efb3996a1289\"",
			wantErr: "the payload repository may not have a tag or digest specified",
		},
		{
			name: "error - override has invalid digest (too short)",
			args: args{
				override: "reg2/repo2@sha256:e7b80",
				imageRef: imageRef1,
			},
			input:   "image: \"reg/repo@sha256:e7b801fc86f79eefd63b881feb1d2a6dc00e7d79bbd09765a2c3efb3996a1289\"",
			wantErr: "the payload repository is invalid: invalid reference format",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := newManifestOverrideMapper(tt.args.override, tt.args.imageRef)
			if (err != nil) != (len(tt.wantErr) > 0) {
				t.Fatal(err)
			}
			if err != nil {
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error did not contain %q: %v", tt.wantErr, err)
				}
				return
			}
			out, err := m([]byte(tt.input))
			if (err != nil) != (len(tt.wantErr) > 0) {
				t.Fatal(err)
			}
			if err != nil {
				return
			}
			if string(out) != tt.output {
				t.Errorf("unexpected output, wanted\n%s\ngot\n%s", tt.output, string(out))
			}
		})
	}
}
