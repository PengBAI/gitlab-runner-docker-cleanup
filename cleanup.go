package main

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	"github.com/dustin/go-humanize"
	"github.com/fsouza/go-dockerclient"
	"gitlab.com/ayufan/golang-cli-helpers"
	"gitlab.com/gitlab-org/gitlab-ci-multi-runner/helpers/cli"
	"gitlab.com/gitlab-org/gitlab-ci-multi-runner/helpers/docker"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const dockerAPIVersion = "1.18"
const objectPastTimeDivisor = time.Second
const danglingImageBonus = 1000
const spaceAllFree uint64 = ^uint64(0)

var version string = "dev"
var revision string = "dev"

var internalImages = []string{
	"gitlab/gitlab-runner:*",
	"quay.io/gitlab-runner:*",
	"quay.io/gitlab-runner-*:*",
}

var diskSpaceImage = "alpine"

var opts = struct {
	MonitorPath            string        `long:"check-path" description:"Path to monitor when verifying disk space" env:"CHECK_PATH"`
	LowFreeSpace           string        `long:"low-free-space" description:"When to trigger cleanup cycle" env:"LOW_FREE_SPACE"`
	ExpectedFreeSpace      string        `long:"expected-free-space" description:"How much free space to cleanup" env:"EXPECTED_FREE_SPACE"`
	LowFreeFilesCount      uint64        `long:"low-files-count" description:"Trigger cleanup cycle if number of i-nodes runs below this value" env:"LOW_FREE_FILES_COUNT"`
	ExpectedFreeFilesCount uint64        `long:"expected-files-count" description:"How much free i-nodes to recycle" env:"EXPECTED_FREE_FILES_COUNT"`
	UseDf                  bool          `long:"use-df" description:"Use 'df' to check disk space instead of docker container" env:"USE_DF"`
	CheckInterval          time.Duration `long:"check-interval" description:"How often to check disk space?" env:"CHECK_INTERVAL"`
	RetryInterval          time.Duration `long:"retry-interval" description:"How long to wait before trying again?" env:"RETRY_INTERVAL"`
	DefaultTTL             time.Duration `long:"ttl" description:"Default minimum TTL for caches and images" env:"DEFAULT_TTL"`
}{
	"/",
	"1GB",
	"2GB",
	128 * 1024,
	256 * 1024,
	false,
	10 * time.Second,
	30 * time.Second,
	1 * time.Minute,
}

type DiskSpace struct {
	BytesFree, BytesTotal uint64
	FilesFree, FilesTotal uint64
}

type DockerClient interface {
	Ping() error
	ListImages(opts docker.ListImagesOptions) ([]docker.APIImages, error)
	ListContainers(opts docker.ListContainersOptions) ([]docker.APIContainers, error)
	RemoveImage(name string) error
	RemoveContainer(opts docker.RemoveContainerOptions) error
	InspectContainer(id string) (*docker.Container, error)
	DiskSpace(path string) (DiskSpace, error)
}

type CustomDockerClient struct {
	*docker.Client
}

type ObjectTTL struct {
	Used time.Time
	TTL  time.Time
}

func (u *ObjectTTL) mark(ttl time.Duration) {
	u.Used = time.Now()
	u.TTL = u.Used.Add(ttl)
}

func (u *ObjectTTL) score() int64 {
	return int64(time.Now().Sub(u.TTL) / objectPastTimeDivisor)
}

type ImageInfo struct {
	docker.APIImages
	ObjectTTL
}

func (i *ImageInfo) score() int64 {
	s := i.ObjectTTL.score()
	if s > 0 && len(i.RepoTags) == 0 {
		s += danglingImageBonus
	}
	return s
}

type CacheInfo struct {
	docker.APIContainers
	ObjectTTL
}

var dockerCredentials docker_helpers.DockerCredentials
var imagesUsed map[string]ImageInfo = make(map[string]ImageInfo)
var cachesUsed map[string]CacheInfo = make(map[string]CacheInfo)

func (c *CustomDockerClient) diskSpaceLocally(path string) (ds DiskSpace, err error) {
	var stat syscall.Statfs_t
	err = syscall.Statfs(path, &stat)
	if err == nil {
		ds = DiskSpace{
			BytesFree:  stat.Bavail * uint64(stat.Bsize),
			BytesTotal: stat.Blocks * uint64(stat.Bsize),
			FilesFree:  stat.Ffree,
			FilesTotal: stat.Files,
		}
	}
	return
}

func (c *CustomDockerClient) diskSpaceRemotely(path string) (ds DiskSpace, err error) {
	_, err = c.InspectImage(diskSpaceImage)
	if err != nil {
		logrus.Debugln("Pulling", diskSpaceImage, "...")
		err = c.PullImage(docker.PullImageOptions{
			Repository: diskSpaceImage,
		}, docker.AuthConfiguration{})
		if err != nil {
			return
		}
	}

	// create container for the first time
	container, err := c.CreateContainer(docker.CreateContainerOptions{
		Config: &docker.Config{
			Image:        diskSpaceImage,
			Entrypoint:   []string{"/bin/stat"},
			Cmd:          []string{"-f", "-c%a %b %s %d %c", path},
			AttachStdout: true,
		},
	})
	if err != nil {
		return
	}

	defer c.RemoveContainer(docker.RemoveContainerOptions{
		ID:    container.ID,
		Force: true,
	})

	err = c.StartContainer(container.ID, nil)
	if err != nil {
		return
	}

	errorCode, err := c.WaitContainer(container.ID)
	if err != nil || errorCode != 0 {
		return
	}

	var buffer bytes.Buffer
	err = c.Logs(docker.LogsOptions{
		Container:    container.ID,
		OutputStream: &buffer,
		Stdout:       true,
		Tail:         "1",
	})
	if err != nil {
		return
	}

	var freeBlocks, totalBlocks, blockSize, freeFiles, totalFiles uint64
	_, err = fmt.Fscanln(&buffer, &freeBlocks, &totalBlocks, &blockSize, &freeFiles, &totalFiles)
	if err != nil {
		return
	}

	ds = DiskSpace{
		BytesFree:  uint64(freeBlocks * blockSize),
		BytesTotal: uint64(totalBlocks * blockSize),
		FilesFree:  freeFiles,
		FilesTotal: totalFiles,
	}
	return
}

func (c *CustomDockerClient) DiskSpace(path string) (DiskSpace, error) {
	if opts.UseDf {
		return c.diskSpaceLocally(path)
	} else {
		return c.diskSpaceRemotely(path)
	}
}

func isInternalImage(image docker.APIImages) bool {
	for _, tag := range image.RepoTags {
		for _, internalImage := range internalImages {
			if matched, _ := filepath.Match(internalImage, tag); matched {
				return true
			}
		}
	}
	return false
}

func removeImage(client DockerClient, image docker.APIImages) error {
	err := client.RemoveImage(image.ID)
	if err == nil {
		logrus.Infoln("Removed image", image.ID, image.RepoTags)
	} else {
		logrus.Warningln("Failed to remove image", image.ID, image.RepoTags, strings.TrimSpace(err.Error()))
	}
	return err
}

func removeCache(client DockerClient, cache docker.APIContainers) error {
	err := client.RemoveContainer(docker.RemoveContainerOptions{
		ID:            cache.ID,
		RemoveVolumes: true,
		Force:         true,
	})
	if err == nil {
		logrus.Infoln("Removed cache", cache.ID, cache.Names)
	} else {
		logrus.Warningln("Failed to remove cache", cache.ID, cache.Names, strings.TrimSpace(err.Error()))
	}
	return err
}

func handleDockerImageID(client DockerClient, id string) {
	logrus.Debugln("handleDockerImageID", id)
	image, ok := imagesUsed[id]
	if !ok {
		return
	}
	image.mark(opts.DefaultTTL)
	imagesUsed[id] = image
	if image.ParentID != "" {
		handleDockerImageID(client, image.ParentID)
	}
}

func isCacheContainer(names ...string) bool {
	for _, name := range names {
		if strings.Contains(name, "runner-") &&
			strings.Contains(name, "-project-") &&
			strings.Contains(name, "-concurrent-") &&
			strings.Contains(name, "-cache-") {
			return true
		}
	}
	return false
}

func handleDockerContainer(client DockerClient, container *docker.Container) {
	logrus.Debugln("handleDockerContainer", container.Name, container.ID, container.Image, container.State.Running)

	handleDockerImageID(client, container.Image)

	if isCacheContainer(container.Name) {
		if cache, ok := cachesUsed[container.ID]; ok {
			cache.mark(opts.DefaultTTL)
			cachesUsed[container.ID] = cache
		}
		return
	}

	for _, otherContainer := range container.HostConfig.VolumesFrom {
		handleDockerContainerID(client, otherContainer)
	}
	for _, otherContainer := range container.HostConfig.Links {
		containerAndAlias := strings.SplitN(otherContainer, ":", 2)
		if len(containerAndAlias) < 1 {
			continue
		}
		handleDockerContainerID(client, containerAndAlias[0])
	}
}

func handleDockerContainerID(client DockerClient, containerID string) {
	container, err := client.InspectContainer(containerID)
	if err != nil {
		logrus.Warningln("Failed to inspect container", containerID, err)
		return
	}
	handleDockerContainer(client, container)
}

func updateImages(client DockerClient) error {
	newUsed := make(map[string]ImageInfo)

	// traverse all images
	images, err := client.ListImages(docker.ListImagesOptions{
		All: true,
	})
	if err != nil {
		return err
	}
	for _, image := range images {
		imageInfo := ImageInfo{
			APIImages: image,
		}
		if imageUsed, ok := imagesUsed[image.ID]; ok {
			imageInfo.ObjectTTL = imageUsed.ObjectTTL
		} else {
			logrus.Infoln("Detected a new image", image.ID, image.RepoTags)
			imageInfo.mark(opts.DefaultTTL)
		}
		newUsed[image.ID] = imageInfo
	}
	imagesUsed = newUsed
	return nil
}

func updateContainers(client DockerClient) error {
	// traverse all running containers
	containers, err := client.ListContainers(docker.ListContainersOptions{
		All: true,
	})
	if err != nil {
		return err
	}

	newCaches := make(map[string]CacheInfo)

	// detect caches
	for _, container := range containers {
		if !isCacheContainer(container.Names...) {
			continue
		}

		cacheInfo := CacheInfo{
			APIContainers: container,
		}
		if cacheUsed, ok := cachesUsed[container.ID]; ok {
			cacheInfo.ObjectTTL = cacheUsed.ObjectTTL
		} else {
			logrus.Infoln("Detected a new cache", container.ID, container.Names)
			cacheInfo.mark(opts.DefaultTTL)
		}
		newCaches[container.ID] = cacheInfo
	}
	cachesUsed = newCaches

	// traverse all other containers to mark images and caches as used
	for _, container := range containers {
		if isCacheContainer(container.Names...) {
			continue
		}
		handleDockerContainerID(client, container.ID)
	}
	return nil
}

func doFreeSpace(client DockerClient, freeSpace, freeFiles uint64) error {
	images, err := client.ListImages(docker.ListImagesOptions{})
	if err != nil {
		logrus.Warningln("Failed to list images:", err)
		return err
	}

	containers, err := client.ListContainers(docker.ListContainersOptions{
		All: true,
	})
	if err != nil {
		logrus.Warningln("Failed to list containers:", err)
		return err
	}

	var lastError error
	for {
		diskSpace, err := client.DiskSpace(opts.MonitorPath)
		if err != nil {
			return err
		}
		if diskSpace.BytesFree > freeSpace && diskSpace.FilesFree > freeFiles {
			break
		}

		var bestScore int64 = -1
		bestImageIndex := -1
		bestCacheIndex := -1

		for idx, image := range images {
			if isInternalImage(image) {
				continue
			}
			if imageInfo, ok := imagesUsed[image.ID]; ok {
				score := imageInfo.score()
				if score > bestScore {
					bestImageIndex = idx
					bestCacheIndex = -1
					bestScore = score
				}
			}
		}

		for idx, container := range containers {
			if !isCacheContainer(container.Names...) {
				continue
			}
			if cacheInfo, ok := cachesUsed[container.ID]; ok {
				score := cacheInfo.score()
				if score > bestScore {
					bestImageIndex = -1
					bestCacheIndex = idx
					bestScore = score
				}
			}
		}

		logrus.Debugln("doFreeCycle", bestScore, bestImageIndex, bestCacheIndex)

		if bestImageIndex >= 0 {
			lastError = removeImage(client, images[bestImageIndex])
			images = append(images[0:bestImageIndex], images[bestImageIndex+1:len(images)]...)
		} else if bestCacheIndex >= 0 {
			lastError = removeCache(client, containers[bestCacheIndex])
			containers = append(containers[0:bestCacheIndex], containers[bestCacheIndex+1:len(containers)]...)
		} else {
			lastError = errors.New("no images or caches to delete")
			break
		}
	}
	return lastError
}

func doCycle(client DockerClient, lowFreeSpace, freeSpace, lowFreeFiles, freeFiles uint64) error {
	err := updateImages(client)
	if err != nil {
		logrus.Warningln("Failed to update images:", err)
		return err
	}

	err = updateContainers(client)
	if err != nil {
		logrus.Warningln("Failed to update caches:", err)
		return err
	}

	diskSpace, err := client.DiskSpace(opts.MonitorPath)
	if err != nil {
		logrus.Warningln("Failed to verify disk space:", err)
		return err
	}
	if diskSpace.BytesFree >= lowFreeSpace && diskSpace.FilesFree >= lowFreeFiles {
		if diskSpace.BytesFree >= lowFreeSpace {
			logrus.Debugln("Nothing to free. Current free disk space", humanize.Bytes(diskSpace.BytesFree),
				"is above the lower bound", humanize.Bytes(lowFreeSpace))
		}
		if diskSpace.FilesFree >= lowFreeFiles {
			logrus.Debugln("Nothing to free. Current free files count", diskSpace.FilesFree,
				"is above the lower bound", lowFreeFiles)
		}
		return nil
	}

	if diskSpace.BytesFree < lowFreeSpace {
		logrus.Warningln("Freeing disk space. The disk space is below the lower bound:", humanize.Bytes(diskSpace.BytesFree),
			"trying to free up to:", humanize.Bytes(freeSpace))
	}
	if diskSpace.FilesFree < lowFreeSpace {
		logrus.Warningln("Freeing files count. The free file count is below the lower bound:", diskSpace.FilesFree,
			"trying to free up to:", freeFiles)
	}

	freeSpaceErr := doFreeSpace(client, freeSpace, freeFiles)
	if freeSpaceErr != nil {
		logrus.Warningln("Failed to free disk space:", freeSpaceErr)
	}

	currentDiskSpace, err := client.DiskSpace(opts.MonitorPath)
	if err == nil {
		logrus.Infoln("Freed",
			"bytes:", humanize.Bytes(currentDiskSpace.BytesFree-diskSpace.BytesFree),
			"files:", currentDiskSpace.FilesFree-diskSpace.FilesFree)
	}

	return freeSpaceErr
}

func runCleanupTool(c *cli.Context) {
	lowFreeSpace, err := humanize.ParseBytes(opts.LowFreeSpace)
	if err != nil {
		logrus.Fatalln(err)
	}

	expectedFreeSpace, err := humanize.ParseBytes(opts.ExpectedFreeSpace)
	if err != nil {
		logrus.Fatalln(err)
	}

	var dockerClient DockerClient

	logrus.Infoln("Watching disk space...")
	for {
		if dockerClient == nil || dockerClient.Ping() != nil {
			dockerClient = nil

			client, err := docker_helpers.Connect(dockerCredentials, dockerAPIVersion)
			if err != nil {
				logrus.Warningln("Failed to connect to daemon:", err)
				time.Sleep(opts.RetryInterval)
				continue
			}

			dockerClient = &CustomDockerClient{
				Client: client,
			}
		}

		err = doCycle(dockerClient, lowFreeSpace, expectedFreeSpace, opts.LowFreeFilesCount, opts.ExpectedFreeFilesCount)
		if err == nil {
			time.Sleep(opts.CheckInterval)
		} else {
			time.Sleep(opts.RetryInterval)
		}
	}
}

func main() {
	app := cli.NewApp()
	app.Name = path.Base(os.Args[0])
	app.Usage = "a GitLab Runner Docker Image Cleanup Tool"
	app.Version = fmt.Sprintf("%s (%s)", version, revision)
	app.Author = "Kamil Trzciński"
	app.Email = "ayufan@ayufan.eu"
	cli_helpers.SetupLogLevelOptions(app)
	app.Flags = append(app.Flags, clihelpers.GetFlagsFromStruct(&dockerCredentials, "docker")...)
	app.Flags = append(app.Flags, clihelpers.GetFlagsFromStruct(&opts)...)
	app.Action = runCleanupTool
	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}
