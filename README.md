# sliderepl for go

Inspired by http://discorporate.us/projects/sliderepl/ for Python, I wanted to have similar funcitonality for Go, similar to the [the Go tour](http://tour.golang.org/) and [Go Play](http://play.golang.org), but self-contained and suitable for presentations.

So, I modified the `go/misc/goplay/goplay.go` file to support the concept of slides.

I also made it so that the slides didn't have to be full Go programs, but can be snippets that are automatically made into propery `func main()`-like programs (with imports being fixed).

## How to use

* Put your slides in `slides.go` (or point to the file with `--slides=`)
* Run `go run sliderepl.go`
* Go to `http://localhost:3999`

## Notes

* Slides are separated by `//!`.
* Empty slides are ignored.
* There is a next/previous button on the page

## Example


```go
//!
// This is the basic hello, world program.
import "fmt"

fmt.Println("hello, world")

//!
// A simple for-loop
import "fmt"

for i := 0; i < 10; i++ {
	fmt.Printf("i = %v\n", i)
}
//!
// This is just a comment slide
```

# License

Licensed under a BSD-style license, since it is based on `goplay.go`.

