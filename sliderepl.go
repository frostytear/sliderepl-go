// Copyright 2013 Christopher Swenson. All rights reserved.
// Copyright 2010 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"text/template"
)

type Slide struct {
	Contents string
	Notes    string
}

var (
	httpListen = flag.String("http", "127.0.0.1:3999", "host:port to listen on")
	htmlOutput = flag.Bool("html", false, "render program output as HTML")
	slidesFile = flag.String("slides", "slides.go", "Slides file to read in")
	staticHTML = flag.String("static", "", "write slides to static HTML file")
	slides     []Slide
)

var (
	// a source of numbers, for naming temporary files
	uniq = make(chan int)
)

func main() {
	flag.Parse()

	// source of unique numbers
	go func() {
		for i := 0; ; i++ {
			uniq <- i
		}
	}()

	readSlides()
	if *staticHTML != "" {
		fmt.Println("Writing to file", *staticHTML)
		f, err := os.Create(*staticHTML)
		if err != nil {
			log.Fatal(err)
		}
		err = staticPage.Execute(f, slides)
		f.Close()
	} else {
		http.HandleFunc("/", FrontPage)
		http.HandleFunc("/compile", Compile)
		fmt.Printf("Listening on %s\n", *httpListen)
		log.Fatal(http.ListenAndServe(*httpListen, nil))
	}
}

func readSlides() {
	slidesRaw, err := ioutil.ReadFile(*slidesFile)
	if err != nil {
		panic(err)
	}
	splitSlides := strings.Split(string(slidesRaw), "//!")
	slides = make([]Slide, 0, len(splitSlides))
	for _, slideString := range splitSlides {
		trimmed := strings.TrimSpace(slideString)
		if len(trimmed) == 0 {
			continue
		}
		s := strings.Split(trimmed, "/*--")
		notes := ""
		if len(s) == 2 {
			notes = strings.TrimSuffix(s[1], "*/")
		}
		slides = append(slides, Slide{s[0], notes})
	}
}

type PageData struct {
	Contents  string
	Notes     string
	PrevSlide int64
	NextSlide int64
}

// FrontPage is an HTTP handler that renders the goplay interface.
// If a filename is supplied in the path component of the URI,
// its contents will be put in the interface's text area.
// Otherwise, the default "hello, world" program is displayed.
func FrontPage(w http.ResponseWriter, req *http.Request) {
	data, err := ioutil.ReadFile(req.URL.Path[1:])
	notes := ""
	slide := int64(0)
	if s := req.URL.Query()["s"]; s != nil {
		slide, _ = strconv.ParseInt(s[0], 10, 16)
	}
	var cont string
	if err != nil {
		cont = slides[slide].Contents
		notes = slides[slide].Notes
	} else {
		cont = string(data)
	}
	prevSlide := slide - 1
	if prevSlide < 0 {
		prevSlide = 0
	}
	nextSlide := slide + 1
	if int(nextSlide) >= len(slides) {
		nextSlide = slide
	}
	params := PageData{cont, notes, prevSlide, nextSlide}
	frontPage.Execute(w, params)
}

// Compile is an HTTP handler that reads Go source code from the request,
// runs the program (returning any errors),
// and sends the program's output as the HTTP response.
func Compile(w http.ResponseWriter, req *http.Request) {
	out, err := compile(req)
	if err != nil {
		error_(w, out, err)
		return
	}

	// write the output of x as the http response
	if *htmlOutput {
		w.Write(out)
	} else {
		output.Execute(w, out)
	}
}

var (
	commentRe = regexp.MustCompile(`(?m)^#.*\n`)
	packageRe = regexp.MustCompile(`^package`)
	importRe  = regexp.MustCompile(`\nimport .*`)
	tmpdir    string
)

func init() {
	// find real temporary directory (for rewriting filename in output)
	var err error
	tmpdir, err = filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		log.Fatal(err)
	}
}

func compile(req *http.Request) (out []byte, err error) {
	// x is the base name for .go, .6, executable files
	x := filepath.Join(tmpdir, "compile"+strconv.Itoa(<-uniq))
	src := x + ".go"
	bin := x
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	// rewrite filename in error output
	defer func() {
		if err != nil {
			// drop messages from the go tool like '# _/compile0'
			out = commentRe.ReplaceAll(out, nil)
		}
		out = bytes.Replace(out, []byte(src+":"), []byte("main.go:"), -1)
	}()

	// write body to x.go
	body := new(bytes.Buffer)
	if _, err = body.ReadFrom(req.Body); err != nil {
		return
	}

	originalBodyBytes := body.Bytes()
	var bodyBytes []byte

	// check to see if the body starts with a "package"
	if packageRe.Find(originalBodyBytes) == nil {
		newBody := new(bytes.Buffer)
		newBody.WriteString("package main\n")
		// move all import lines to the top
		for _, importLine := range importRe.FindAll(originalBodyBytes, -1) {
			newBody.Write(importLine)
			newBody.WriteRune(10)
		}
		newBody.WriteString("func main() {\n")
		newBody.Write(importRe.ReplaceAll(originalBodyBytes, make([]byte, 0)))
		newBody.WriteString("\n}\n")
		bodyBytes = newBody.Bytes()
	} else {
		bodyBytes = originalBodyBytes
	}

	defer os.Remove(src)
	if err = ioutil.WriteFile(src, bodyBytes, 0666); err != nil {
		return
	}

	// build x.go, creating x
	dir, file := filepath.Split(src)
	out, err = run(dir, "go", "build", "-o", bin, file)
	defer os.Remove(bin)
	if err != nil {
		return
	}

	// run x
	return run("", bin)
}

// error writes compile, link, or runtime errors to the HTTP connection.
// The JavaScript interface uses the 404 status code to identify the error.
func error_(w http.ResponseWriter, out []byte, err error) {
	w.WriteHeader(404)
	if out != nil {
		output.Execute(w, out)
	} else {
		output.Execute(w, err.Error())
	}
}

// run executes the specified command and returns its output and an error.
func run(dir string, args ...string) ([]byte, error) {
	var buf bytes.Buffer
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Stdout = &buf
	cmd.Stderr = cmd.Stdout
	err := cmd.Run()
	return buf.Bytes(), err
}

var frontPage = template.Must(template.New("frontPage").Parse(frontPageText)) // HTML template
var staticPage = template.Must(template.New("staticPage").Parse(staticPageText))
var output = template.Must(template.New("output").Parse(outputText)) // HTML template

var outputText = `<pre>{{printf "%s" . |html}}</pre>`

var staticPageText = `<!doctype html>
<html>
<head>
<style>
.notes .slide {
	font-family: Monaco, 'Courier New', 'DejaVu Sans Mono', 'Bitstream Vera Sans Mono', monospace;
}
.slide td {
	border: 1px solid black;
	height: 400px;
}
.controls td {
	height: 20px;
}
.notes td {
	height: 80px;
}
td {
	width: 800px;
	vertical-align: text-top;
	padding: 10px;
}

</style>
<script>

function toggleNotes() {
	if (noteState=="none") {
		noteState = "inline"
		document.cookie="notes=true"
		document.getElementById("noteButton").innerHTML = "Hide notes"
	} else {
		noteState = "none"
		document.cookie="notes="
		document.getElementById("noteButton").innerHTML = "Show notes"
	}
	displaySlide()
}

function next() {
	currentSlide++;
	displaySlide();
}

function prev() {
	currentSlide--;
	displaySlide();
}

function displaySlide() {
	numSlides = document.getElementsByClassName("slide").length
	if (currentSlide < 0) {
		currentSlide = 0;
	}
	if (currentSlide >= numSlides) {
		currentSlide = numSlides-1;
	}
	for (i=0; i<numSlides; i++) {
		if (i==currentSlide) {
			document.getElementById("slide_"+i).style.display="inline"
			if (noteState=="inline") {
				document.getElementById("notes_"+i).style.display="inline"
			} else {
				document.getElementById("notes_"+i).style.display="none"
			}
		} else {
			document.getElementById("slide_"+i).style.display="none"
			document.getElementById("notes_"+i).style.display="none"
		}
	}
}

function onPageLoad() {
	currentSlide = 0
	noteState = "none"
	var c = document.cookie;
	if (c.search("notes=true")>=0) {
		toggleNotes()
	}
	displaySlide()
}

</script>
</head>
<body onload="onPageLoad()">
<table>
{{range $i, $contents := .}}
<tr class="slide" id="slide_{{printf "%d" $i }}"><td><pre>{{printf "%s" $contents.Contents |html}}</pre></td></tr>
{{end}}
<tr class="controls"><td>
<button id="noteButton" onclick="toggleNotes()">Show notes</button>
<button onclick="prev()">Previous</button>
<button onclick="next()">Next</button>
</td></tr>
{{range $i, $contents := .}}
<tr class="notes" id="notes_{{printf "%d" $i }}"><td>
<pre>{{printf "%s" $contents.Notes |html}}</pre>
</td></tr>
{{end}}
</table>

</body>
</html>
`

var frontPageText = `<!doctype html>
<html>
<head>
<style>
pre, textarea {
	font-family: Monaco, 'Courier New', 'DejaVu Sans Mono', 'Bitstream Vera Sans Mono', monospace;
	font-size: 100%;
}
#notes {
	font-family: Monaco, 'Courier New', 'DejaVu Sans Mono', 'Bitstream Vera Sans Mono', monospace;
	font-size: 50%;
}
.hints {
	font-size: 0.8em;
	text-align: right;
}
#edit, #output, #errors { width: 100%; text-align: left; }
#edit { height: 500px; }
#output { color: #00c; }
#errors { color: #c00; }
</style>
<script>

function insertTabs(n) {
	// find the selection start and end
	var cont  = document.getElementById("edit");
	var start = cont.selectionStart;
	var end   = cont.selectionEnd;
	// split the textarea content into two, and insert n tabs
	var v = cont.value;
	var u = v.substr(0, start);
	for (var i=0; i<n; i++) {
		u += "\t";
	}
	u += v.substr(end);
	// set revised content
	cont.value = u;
	// reset caret position after inserted tabs
	cont.selectionStart = start+n;
	cont.selectionEnd = start+n;
}

function autoindent(el) {
	var curpos = el.selectionStart;
	var tabs = 0;
	while (curpos > 0) {
		curpos--;
		if (el.value[curpos] == "\t") {
			tabs++;
		} else if (tabs > 0 || el.value[curpos] == "\n") {
			break;
		}
	}
	setTimeout(function() {
		insertTabs(tabs);
	}, 1);
}

function preventDefault(e) {
	if (e.preventDefault) {
		e.preventDefault();
	} else {
		e.cancelBubble = true;
	}
}

function keyHandler(event) {
	var e = window.event || event;
	if (e.keyCode == 9) { // tab
		insertTabs(1);
		preventDefault(e);
		return false;
	}
	if (e.keyCode == 13) { // enter
		if (e.shiftKey) { // +shift
			compile(e.target);
			preventDefault(e);
			return false;
		} else {
			autoindent(e.target);
		}
	}
	return true;
}

var xmlreq;

function autocompile() {
	if(!document.getElementById("autocompile").checked) {
		return;
	}
	compile();
}

function compile() {
	var prog = document.getElementById("edit").value;
	var req = new XMLHttpRequest();
	xmlreq = req;
	req.onreadystatechange = compileUpdate;
	req.open("POST", "/compile", true);
	req.setRequestHeader("Content-Type", "text/plain; charset=utf-8");
	req.send(prog);	
}

function compileUpdate() {
	var req = xmlreq;
	if(!req || req.readyState != 4) {
		return;
	}
	if(req.status == 200) {
		document.getElementById("output").innerHTML = req.responseText;
		document.getElementById("errors").innerHTML = "";
	} else {
		document.getElementById("errors").innerHTML = req.responseText;
		document.getElementById("output").innerHTML = "";
	}
}

function toggleNotes() {
	state = document.getElementById("notes").style.display
	if (state=="none") {
		document.getElementById("notes").style.display = ""
		document.cookie="notes=true"
		document.getElementById("noteButton").innerHTML = "Hide notes"
	} else {
		document.getElementById("notes").style.display = "none"
		document.cookie="notes="
		document.getElementById("noteButton").innerHTML = "Show notes"
	}
}

function onPageLoad() {
	var c = document.cookie;
	if (c.search("notes=true")<0) {
		toggleNotes()
	}
}
</script>
</head>
<body onload="onPageLoad()">
<table width="100%"><tr><td width="60%" valign="top">
<textarea autofocus="true" id="edit" spellcheck="false" onkeydown="keyHandler(event);" onkeyup="autocompile();">{{printf "%s" .Contents |html}}</textarea>
<div class="hints">
(Shift-Enter to compile and run.)&nbsp;&nbsp;&nbsp;&nbsp;
<input type="checkbox" id="autocompile" value="checked" /> Compile and run after each keystroke
<button id="noteButton" onclick="toggleNotes()">Hide notes</button>
<button onclick="window.location.href = '/?s={{ printf "%d" .PrevSlide }}'">Previous</button>
<button onclick="window.location.href = '/?s={{ printf "%d" .NextSlide }}'">Next</button>

</div>
<td width="3%">
<td width="27%" align="right" valign="top">
<div id="output"></div>
</table>
<div id="errors"></div>
<div id="notes">{{ printf "%s" .Notes |html}}</div>
</body>
</html>
`
