FROM golang:1.4

RUN git clone https://gitlab.com/gitlab-org/gitlab-ci-multi-runner.git \
	/go/src/gitlab.com/gitlab-org/gitlab-ci-multi-runner \
	-b master --depth 1

COPY . /go/src/gitlab-runner-docker-cleanup
WORKDIR /go/src/gitlab-runner-docker-cleanup
RUN go-wrapper download
RUN go-wrapper install
RUN go-wrapper download gopkg.in/check.v1
RUN go test
ENTRYPOINT ["go-wrapper", "run"]
