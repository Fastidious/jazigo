#!/bin/sh

src=`find . -type f | egrep '\.go$'`

gofmt -s -w $src
go tool vet $src
go tool fix $src
go install github.com/udhos/jazigo/jazigo

# go get honnef.co/go/simple/cmd/gosimple
s=$GOPATH/bin/gosimple
simple() {
    # gosimple cant handle source files from multiple packages
    $s jazigo/*.go
    $s conf/*.go
    $s dev/*.go
    $s store/*.go
    $s temp/*.go
}
[ -x "$s" ] && simple

go test github.com/udhos/jazigo/dev
go test github.com/udhos/jazigo/store
