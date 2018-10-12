FROM golang:1.9.4

RUN git clone https://gitlab.com/gitlab-org/gitlab-runner.git \
	/go/src/gitlab.com/gitlab-org/gitlab-runner \
	-b master --depth 1

COPY . /go/src/gitlab-runner-docker-cleanup
WORKDIR /go/src/gitlab-runner-docker-cleanup
RUN go get -v -d
RUN go install
RUN go get -v -d gopkg.in/check.v1
RUN go test

ENTRYPOINT ["go", "run", "cleanup.go"]
