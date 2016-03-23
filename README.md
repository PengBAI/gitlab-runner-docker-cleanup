## GitLab Runner Docker Cleanup

This is simple docker application that automatically garbage collects the GitLab Runner Caches and Images when running on low disk space.

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
    -v /var/run/docker.sock:/var/run/docker.sock \
    --name=gitlab-runner-docker-cleanup \
    quay.io/gitlab/gitlab-runner-docker-cleanup
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
| USE_DF                    | false | Use a command line `df` tool to check disk space. Set to `false` when connecting to remote Docker Engine. Set to `true` when using with locally installed Docker Engine |
| CHECK_INTERVAL            | 10s   | How often to check the disk space |
| RETRY_INTERVAL            | 30s   | How long to wait before retrying in case of failure |
| DEFAULT_TTL               | 1m    | Minimum time to preserve a newly downloaded images or created caches |

## Automated build

The image is automatically built by `quay.io`.
To see a latest build status, go to: 

## Development

1. Install Go Runtime, the 1.5.x is preferred

2. Download the sources with dependencies:

```
go get gitlab.com/gitlab-org/gitlab-runner-docker-cleanup
```

3. Modifying sources and run tests:

```
cd $GOPATH/src/gitlab.com/gitlab-org/gitlab-runner-docker-cleanup
go test
````

## Author

Kamil Trzciński

## License

MIT

