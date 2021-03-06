// Copyright 2018 Google LLC All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package publish

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/google/ko/pkg/build"
)

// defalt is intentionally misspelled to avoid keyword collision (and drive Jon nuts).
type defalt struct {
	base     string
	t        http.RoundTripper
	auth     authn.Authenticator
	namer    Namer
	tags     []string
	insecure bool
}

// Option is a functional option for NewDefault.
type Option func(*defaultOpener) error

type defaultOpener struct {
	base     string
	t        http.RoundTripper
	auth     authn.Authenticator
	namer    Namer
	tags     []string
	insecure bool
}

// Namer is a function from a supported import path to the portion of the resulting
// image name that follows the "base" repository name.
type Namer func(string) string

// identity is the default namer, so import paths are affixed as-is under the repository
// name for maximum clarity, e.g.
//   gcr.io/foo/github.com/bar/baz/cmd/blah
//   ^--base--^ ^-------import path-------^
func identity(in string) string { return in }

// As some registries do not support pushing an image by digest, the default tag for pushing
// is the 'latest' tag.
var defaultTags = []string{"latest"}

func (do *defaultOpener) Open() (Interface, error) {
	return &defalt{
		base:     do.base,
		t:        do.t,
		auth:     do.auth,
		namer:    do.namer,
		tags:     do.tags,
		insecure: do.insecure,
	}, nil
}

// NewDefault returns a new publish.Interface that publishes references under the provided base
// repository using the default keychain to authenticate and the default naming scheme.
func NewDefault(base string, options ...Option) (Interface, error) {
	do := &defaultOpener{
		base:  base,
		t:     http.DefaultTransport,
		auth:  authn.Anonymous,
		namer: identity,
		tags:  defaultTags,
	}

	for _, option := range options {
		if err := option(do); err != nil {
			return nil, err
		}
	}

	return do.Open()
}

func pushResult(tag name.Tag, br build.Result, opt []remote.Option) error {
	mt, err := br.MediaType()
	if err != nil {
		return err
	}

	switch mt {
	case types.OCIImageIndex, types.DockerManifestList:
		idx, ok := br.(v1.ImageIndex)
		if !ok {
			return fmt.Errorf("failed to interpret result as index: %v", br)
		}
		return remote.WriteIndex(tag, idx, opt...)
	case types.OCIManifestSchema1, types.DockerManifestSchema2:
		img, ok := br.(v1.Image)
		if !ok {
			return fmt.Errorf("failed to interpret result as image: %v", br)
		}
		return remote.Write(tag, img, opt...)
	default:
		return fmt.Errorf("result image media type: %s", mt)
	}
}

// Publish implements publish.Interface
func (d *defalt) Publish(br build.Result, s string) (name.Reference, error) {
	s = strings.TrimPrefix(s, build.StrictScheme)
	// https://github.com/google/go-containerregistry/issues/212
	s = strings.ToLower(s)

	ro := []remote.Option{remote.WithAuth(d.auth), remote.WithTransport(d.t)}
	no := []name.Option{}
	if d.insecure {
		no = append(no, name.Insecure)
	}

	for i, tagName := range d.tags {
		tag, err := name.NewTag(fmt.Sprintf("%s/%s:%s", d.base, d.namer(s), tagName), no...)
		if err != nil {
			return nil, err
		}

		if i == 0 {
			log.Printf("Publishing %v", tag)
			if err := pushResult(tag, br, ro); err != nil {
				return nil, err
			}
		} else {
			log.Printf("Tagging %v", tag)
			if err := remote.Tag(tag, br, ro...); err != nil {
				return nil, err
			}
		}
	}

	h, err := br.Digest()
	if err != nil {
		return nil, err
	}
	ref := fmt.Sprintf("%s/%s@%s", d.base, d.namer(s), h)
	if len(d.tags) == 1 && d.tags[0] != defaultTags[0] {
		// If a single tag is explicitly set (not latest), then this
		// is probably a release, so include the tag in the reference.
		ref = fmt.Sprintf("%s/%s:%s@%s", d.base, d.namer(s), d.tags[0], h)
	}
	dig, err := name.NewDigest(ref)
	if err != nil {
		return nil, err
	}
	log.Printf("Published %v", dig)
	return &dig, nil
}

func (d *defalt) Close() error {
	return nil
}
