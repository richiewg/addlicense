// Copyright 2018 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// This program ensures source code files have copyright license headers.
// See usage with "addlicence -h".
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

const helpText = `Usage: addlicense [flags] pattern [pattern ...]

The program ensures source code files have copyright license headers
by scanning directory patterns recursively.

It modifies all source files in place and avoids adding a license header
to any file that already has one.

The pattern argument can be provided multiple times, and may also refer
to single files.

Flags:
`

var (
	holder   = flag.String("c", "xtar", "copyright holder")
	license  = flag.String("l", "glp", "license type: apache, bsd, mit")
	licensef = flag.String("f", "", "license file")
	year     = flag.String("y", fmt.Sprint(time.Now().Year()), "copyright year(s)")
)

type copyrightData struct {
	Year   string
	Holder string
}

func main() {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, helpText)
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(1)
	}

	data := &copyrightData{
		Year:   *year,
		Holder: *holder,
	}

	var t *template.Template
	if *licensef != "" {
		d, err := ioutil.ReadFile(*licensef)
		if err != nil {
			log.Printf("license file: %v", err)
			os.Exit(1)
		}
		t, err = template.New("").Parse(string(d))
		if err != nil {
			log.Printf("license file: %v", err)
			os.Exit(1)
		}
	} else {
		t = licenseTemplate[*license]
		if t == nil {
			log.Printf("unknown license: %s", *license)
			os.Exit(1)
		}
	}

	// process at most 1000 files in parallel
	ch := make(chan *file, 1000)
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for f := range ch {
			wg.Add(1)
			go func(f *file) {
				err := addLicense(f.path, f.mode, t, data)
				if err != nil {
					log.Printf("%s: %v", f.path, err)
				}
				wg.Done()
			}(f)
		}
		wg.Wait()
		close(done)
	}()

	for _, d := range flag.Args() {
		walk(ch, d)
	}
	close(ch)
	<-done
}

type file struct {
	path string
	mode os.FileMode
}

func walk(ch chan<- *file, start string) {
	filepath.Walk(start, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			log.Printf("%s error: %v", path, err)
			return nil
		}
		if fi.IsDir() {
			return nil
		}
		ch <- &file{path, fi.Mode()}
		return nil
	})
}

func addLicense(path string, fmode os.FileMode, tmpl *template.Template, data *copyrightData) error {
	var lic []byte
	var err error
	switch fileExtension(path) {
	default:
		return nil
	case ".c", ".h",".go":
		lic, err = prefix(tmpl, data, "/*", " * ", " */")
	case ".js", ".jsx", ".tsx", ".css", ".tf":
		lic, err = prefix(tmpl, data, "/**", " * ", " */")
	case ".cc", ".cpp", ".cs", ".hh", ".hpp", ".java", ".m", ".mm", ".proto", ".rs", ".scala", ".swift", ".dart", ".groovy":
		lic, err = prefix(tmpl, data, "", "// ", "")
	case ".py", ".sh", ".yaml", ".yml", ".dockerfile", "dockerfile", ".rb", "gemfile":
		lic, err = prefix(tmpl, data, "", "# ", "")
	case ".el", ".lisp":
		lic, err = prefix(tmpl, data, "", ";; ", "")
	case ".erl":
		lic, err = prefix(tmpl, data, "", "% ", "")
	case ".hs", ".sql":
		lic, err = prefix(tmpl, data, "", "-- ", "")
	case ".html", ".xml":
		lic, err = prefix(tmpl, data, "<!--", " ", "-->")
	case ".php":
		lic, err = prefix(tmpl, data, "<?php", "// ", "?>")
	}
	if err != nil || lic == nil {
		return err
	}

	b, err := ioutil.ReadFile(path)
	if err != nil || hasLicense(b) {
		return err
	}

	line := hashBang(b)
	if len(line) > 0 {
		b = b[len(line):]
		if line[len(line)-1] != '\n' {
			line = append(line, '\n')
		}
		lic = append(line, lic...)
	}
	b = append(lic, b...)
	return ioutil.WriteFile(path, b, fmode)
}

func fileExtension(name string) string {
	if v := filepath.Ext(name); v != "" {
		return strings.ToLower(v)
	}
	return strings.ToLower(filepath.Base(name))
}

var head = []string{
	"#!",                       // shell script
	"<?xml",                    // XML declaratioon
	"<!doctype",                // HTML doctype
	"# encoding:",              // Ruby encoding
	"# frozen_string_literal:", // Ruby interpreter instruction
}

func hashBang(b []byte) []byte {
	var line []byte
	for _, c := range b {
		line = append(line, c)
		if c == '\n' {
			break
		}
	}
	first := strings.ToLower(string(line))
	for _, h := range head {
		if strings.HasPrefix(first, h) {
			return line
		}
	}
	return nil
}

func hasLicense(b []byte) bool {
	n := 1000
	if len(b) < 1000 {
		n = len(b)
	}
	return bytes.Contains(bytes.ToLower(b[:n]), []byte("copyright"))
}

var licenseTemplate = make(map[string]*template.Template)

func init() {
	licenseTemplate["apache"] = template.Must(template.New("").Parse(tmplApache))
	licenseTemplate["mit"] = template.Must(template.New("").Parse(tmplMIT))
	licenseTemplate["bsd"] = template.Must(template.New("").Parse(tmplBSD))
	licenseTemplate["glp"] = template.Must(template.New("").Parse(tmplGLP))
}

// prefix will execute a license template t with data d
// and prefix the result with top, middle and bottom.
func prefix(t *template.Template, d *copyrightData, top, mid, bot string) ([]byte, error) {
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if top != "" {
		fmt.Fprintln(&out, top)
	}
	s := bufio.NewScanner(&buf)
	for s.Scan() {
		fmt.Fprintln(&out, strings.TrimRightFunc(mid+s.Text(), unicode.IsSpace))
	}
	if bot != "" {
		fmt.Fprintln(&out, bot)
	}
	fmt.Fprintln(&out)
	return out.Bytes(), nil
}

const tmplApache = `Copyright {{.Year}} {{.Holder}}

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.`

const tmplBSD = `Copyright (c) {{.Year}} {{.Holder}} All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.`

const tmplMIT = `Copyright (c) {{.Year}} {{.Holder}}

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
the Software, and to permit persons to whom the Software is furnished to do so,
subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.`

const tmplGLP = `Copyright (C) {{.Year}} The {{.Holder}} Authors
This file is part of The {{.Holder}} library.

The {{.Holder}} is free software: you can redistribute it and/or modify
it under the terms of the GNU Lesser General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

The {{.Holder}} is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Lesser General Public License for more details.

You should have received a copy of the GNU Lesser General Public License
along with The {{.Holder}}.  If not, see <http://www.gnu.org/licenses/>.`
