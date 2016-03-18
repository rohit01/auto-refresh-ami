# Start from a Debian image with the latest version of Go installed
# and a workspace (GOPATH) configured at /go.
FROM golang:1.6-wheezy

# Copy the local package files to the container's workspace.
ADD . /go/src/autorefresh-ami

# Build the outyet command inside the container.
# (You may fetch or manage dependencies here,
# either manually or with a tool like "godep".)
RUN go get github.com/tools/godep
RUN go install github.com/tools/godep
RUN go get golang.org/x/sys/unix
RUN godep get autorefresh-ami
RUN cd /go/src/autorefresh-ami && godep go install .

# Run the outyet command by default when the container starts.
ENTRYPOINT ["/go/bin/autorefresh-ami"]
CMD        ["-c=/go/src/autorefresh-ami/config/"]
