[![Docker Hub](https://img.shields.io/badge/docker-ready-blue.svg)](https://hub.docker.com/r/pengbai/gitlab-runner-docker-cleanup/)

## GitLab Runner Docker Cleanup

Project forked from [gitlab-org/gitlab-runner-docker-cleanup](https://gitlab.com/gitlab-org/gitlab-runner-docker-cleanup). Thanks to the original project owner.

This is simple docker application that automatically garbage collects the GitLab Runner Caches and Images when running on low disk space. Same require features are implemented in this project.

### New features:

* Update go version to 1.9
* Be able to define a protected internal images list in a file to prevent from removing, your builder image for example.
* Support to remove multi-tag images


## How to run it?

The tool requires access to Docker Engine.

By default the Docker Engine listens under `/var/run/docker.sock`

```
docker run -d \
    -e LOW_FREE_SPACE=10G \
    -e EXPECTED_FREE_SPACE=20G \
    -e LOW_FREE_FILES_COUNT=1048576 \
    -e EXPECTED_FREE_FILES_COUNT=2097152 \
    -e DEFAULT_TTL=10m \
    -e USE_DF=1 \
    --restart always \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v /etc/gitlab_runner_docker_cleanup_internal_images:/etc/gitlab_runner_docker_cleanup_internal_images \
    --name=gitlab-runner-docker-cleanup \
    pengbai/gitlab-runner-docker-cleanup
```

example of /etc/gitlab_runner_docker_cleanup_internal_images

```
$ cat /etc/gitlab_runner_docker_cleanup_internal_images
golang:l.8.4
tutum/curl:alpine
alpine:*
```

The above command will ensure to always have at least `10GB` of free disk space and at least `1M` of free files (i-nodes) on disk.

The i-nodes is especially important when using Docker with `overlay` storage engine.
More information about **i-node** problem [here](http://blog.cloud66.com/docker-with-overlayfs-first-impression/).

## Options

You can configure GitLab Runner Docker Cleanup with environment variables:

| Variable | Default | Description |
| -------- | ------- | ----------- |
| CHECK_PATH                | /     | The path which is used when checking disk usage |
| LOW_FREE_SPACE            | 1GB   | When trigger the cache and image removal |
| EXPECTED_FREE_SPACE       | 2GB   | How much the free space to cleanup |
| LOW_FREE_FILES_COUNT      | 131072| When the number of free files (i-nodes) runs below this value trigger the cache and image removal |
| EXPECTED_FREE_FILES_COUNT | 262144| How many free files (i-nodes) to cleanup |
| USE_DF                    | true | Use a command line `df` tool to check disk space. Set to `false` when connecting to remote Docker Engine. Set to `true` when using with locally installed Docker Engine |
| CHECK_INTERVAL            | 10s   | How often to check the disk space |
| RETRY_INTERVAL            | 30s   | How long to wait before retrying in case of failure |
| DEFAULT_TTL               | 1m    | Minimum time to preserve a newly downloaded images or created caches |
| ADDITIONAL_INTERNAL_IMAGES_FILE_PATH | /etc/gitlab_runner_docker_cleanup_internal_images | User defined images not to remove |
