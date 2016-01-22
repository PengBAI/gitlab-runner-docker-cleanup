package main

import (
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/dustin/go-humanize"
	. "github.com/fsouza/go-dockerclient"
	. "gopkg.in/check.v1"
	"testing"
	"time"
)

type MockDockerClient struct {
	error             error
	removedImages     []string
	removedContainers []string
	containers        []APIContainers
	images            []APIImages
	volumesFrom       []string
	links             []string
	freeSpace         uint64
	totalSpace        uint64
	freeFiles         uint64
	totalFiles        uint64
}

func (c *MockDockerClient) Ping() error {
	return c.error
}

func (c *MockDockerClient) RemoveImage(name string) error {
	if c.error != nil {
		return c.error
	}
	for _, image := range c.images {
		if image.ID == name {
			c.freeSpace += uint64(image.Size)
			c.freeFiles += uint64(image.Size / 4096)
		}
	}
	c.removedImages = append(c.removedImages, name)
	return nil
}

func (c *MockDockerClient) RemoveContainer(opts RemoveContainerOptions) error {
	if c.error != nil {
		return c.error
	}
	for _, container := range c.containers {
		if container.ID == opts.ID {
			c.freeSpace += uint64(container.SizeRw)
			c.freeFiles += uint64(container.SizeRw / 4096)
		}
	}
	c.removedContainers = append(c.removedContainers, opts.ID)
	return nil
}

func (c *MockDockerClient) ListImages(opts ListImagesOptions) ([]APIImages, error) {
	return c.images, c.error
}

func (c *MockDockerClient) ListContainers(opts ListContainersOptions) ([]APIContainers, error) {
	return c.containers, c.error
}

func (c *MockDockerClient) DiskSpace(path string) (DiskSpace, error) {
	return DiskSpace{
		BytesFree:  c.freeSpace,
		BytesTotal: c.totalSpace,
		FilesFree:  c.freeFiles,
		FilesTotal: c.totalFiles,
	}, c.error
}

func (c *MockDockerClient) InspectContainer(id string) (*Container, error) {
	for idx, container := range c.containers {
		if container.ID == id {
			data := &Container{
				ID:              id,
				Name:            id,
				Image:           container.Image,
				HostConfig:      &HostConfig{},
				Config:          &Config{},
				NetworkSettings: &NetworkSettings{},
			}
			if idx == 0 {
				data.HostConfig.VolumesFrom = c.volumesFrom
				data.HostConfig.Links = c.links
			}
			return data, nil
		}
	}
	return nil, &NoSuchContainer{
		ID: id,
	}
}

func Test(t *testing.T) { TestingT(t) }

type CleanupSuite struct {
	dockerClient *MockDockerClient
}

var _ = Suite(&CleanupSuite{})

func (s *CleanupSuite) SetUpTest(c *C) {
	opts.DefaultTTL = 0 * time.Nanosecond
	s.dockerClient = &MockDockerClient{}
	imagesUsed = make(map[string]ImageInfo)
	cachesUsed = make(map[string]CacheInfo)
	logrus.SetLevel(logrus.DebugLevel)
}

func makeDockerImageWithParent(name string, parent string) APIImages {
	return APIImages{
		ID: name,
		RepoTags: []string{
			name,
		},
		ParentID: parent,
	}
}

func makeDockerImageWithSize(name string, size uint64) APIImages {
	return APIImages{
		ID: name,
		RepoTags: []string{
			name,
		},
		ParentID:    "",
		Size:        int64(size),
		VirtualSize: int64(size),
	}
}

func makeDockerImage(name string) APIImages {
	return makeDockerImageWithParent(name, "")
}

func makeDockerContainer(name string, image string) APIContainers {
	return APIContainers{
		ID:    name,
		Image: image,
		Names: []string{name},
	}
}

func makeDockerContainerWithSize(name string, image string, size uint64) APIContainers {
	return APIContainers{
		ID:         name,
		Image:      image,
		Names:      []string{name},
		SizeRw:     int64(size),
		SizeRootFs: int64(size),
	}
}

func makeDockerCache(id string, size uint64) APIContainers {
	return makeDockerContainerWithSize(fmt.Sprintf("runner-RID-project-PID-concurrent-CID-cache-%v", id), "cache", size)
}

func (s *CleanupSuite) TestInternalImage(c *C) {
	cacheImage := isInternalImage(makeDockerImage("gitlab/gitlab-runner:cache"))
	c.Assert(cacheImage, Equals, true)

	rubyImage := isInternalImage(makeDockerImage("ruby:2.1"))
	c.Assert(rubyImage, Equals, false)

	userImage := isInternalImage(makeDockerImage("ayufan/runner:latest"))
	c.Assert(userImage, Equals, false)
}

func (s *CleanupSuite) TestRemoveImage(c *C) {
	err := removeImage(s.dockerClient, makeDockerImage("test"))
	c.Assert(err, IsNil)
	c.Assert(s.dockerClient.removedImages, HasLen, 1)
	c.Assert(s.dockerClient.removedImages[0], Equals, "test")

	s.dockerClient.error = ErrConnectionRefused
	err = removeImage(s.dockerClient, makeDockerImage("error"))
	c.Assert(err, Equals, ErrConnectionRefused)
}

func (s *CleanupSuite) TestCacheContainerDetection(c *C) {
	result := isCacheContainer("runner")
	c.Assert(result, Equals, false)

	result = isCacheContainer("runner-RID-project-PID-concurrent-CID-cache-ID")
	c.Assert(result, Equals, true)

	result = isCacheContainer("runner", "runner-RID-project-PID-concurrent-CID-cache-ID")
	c.Assert(result, Equals, true)
}

func (s *CleanupSuite) TestUpdateImages(c *C) {
	s.dockerClient.images = []APIImages{
		makeDockerImage("test"),
	}
	err := updateImages(s.dockerClient)
	c.Assert(err, IsNil)
	c.Assert(imagesUsed, HasLen, 1)

	testImageInfo := imagesUsed["test"]
	err = updateImages(s.dockerClient)
	c.Assert(err, IsNil)
	c.Assert(imagesUsed, HasLen, 1)
	c.Assert(imagesUsed["test"].ObjectTTL, Equals, testImageInfo.ObjectTTL)

	s.dockerClient.images = []APIImages{
		makeDockerImage("test"),
		makeDockerImage("new"),
	}
	err = updateImages(s.dockerClient)
	c.Assert(err, IsNil)
	c.Assert(imagesUsed, HasLen, 2)

	s.dockerClient.images = []APIImages{
		makeDockerImage("new"),
	}
	err = updateImages(s.dockerClient)
	c.Assert(err, IsNil)
	c.Assert(imagesUsed, HasLen, 1)
}

func (s *CleanupSuite) TestUpdateContainers(c *C) {
	s.dockerClient.containers = []APIContainers{
		makeDockerContainer("other-container", "test"),
	}
	err := updateContainers(s.dockerClient)
	c.Assert(err, IsNil)
	c.Assert(cachesUsed, HasLen, 0)

	s.dockerClient.containers = []APIContainers{
		makeDockerCache("test", humanize.MByte),
	}
	err = updateContainers(s.dockerClient)
	c.Assert(err, IsNil)
	c.Assert(cachesUsed, HasLen, 1)

	testCacheInfo := cachesUsed["test"]
	err = updateContainers(s.dockerClient)
	c.Assert(err, IsNil)
	c.Assert(cachesUsed, HasLen, 1)
	c.Assert(cachesUsed["test"].ObjectTTL, DeepEquals, testCacheInfo.ObjectTTL)

	s.dockerClient.containers = []APIContainers{
		makeDockerCache("test", humanize.MByte),
		makeDockerCache("new", humanize.MByte),
	}
	err = updateContainers(s.dockerClient)
	c.Assert(err, IsNil)
	c.Assert(cachesUsed, HasLen, 2)

	s.dockerClient.containers = []APIContainers{
		makeDockerCache("new", humanize.MByte),
	}
	err = updateContainers(s.dockerClient)
	c.Assert(err, IsNil)
	c.Assert(cachesUsed, HasLen, 1)
}

func (s *CleanupSuite) TestContainerTraversing(c *C) {
	s.dockerClient.images = []APIImages{
		makeDockerImage("test"),
	}
	s.dockerClient.containers = []APIContainers{
		makeDockerContainer("other-container", "test"),
	}

	// first, images needs to be registered
	handleDockerContainerID(s.dockerClient, "other-container")
	c.Assert(imagesUsed, HasLen, 0)

	// register images
	err := updateImages(s.dockerClient)
	c.Assert(err, IsNil)
	c.Assert(imagesUsed, HasLen, 1)

	testImage := imagesUsed["test"]

	// check if image got updated
	handleDockerContainerID(s.dockerClient, "other-container")
	c.Assert(imagesUsed, HasLen, 1)
	c.Assert(imagesUsed["test"].ObjectTTL, Not(DeepEquals), testImage.ObjectTTL)
}

func (s *CleanupSuite) TestVolumesFromHandling(c *C) {
	otherContainer := makeDockerContainer("other-container", "other-image")
	s.dockerClient.volumesFrom = []string{
		otherContainer.ID,
	}
	s.dockerClient.containers = []APIContainers{
		makeDockerContainer("container", "image"),
		otherContainer,
	}
	s.dockerClient.images = []APIImages{
		makeDockerImage("test"),
		makeDockerImage("other-image"),
	}
	updateImages(s.dockerClient)
	c.Assert(imagesUsed, HasLen, 2)
	otherImage := imagesUsed["other-image"]

	handleDockerContainerID(s.dockerClient, "container")
	c.Assert(imagesUsed["otherImage"].ObjectTTL, Not(DeepEquals), otherImage.ObjectTTL)
}

func (s *CleanupSuite) TestLinksHandling(c *C) {
	otherContainer := makeDockerContainer("other-container", "other-image")
	s.dockerClient.links = []string{
		otherContainer.ID,
	}
	s.dockerClient.containers = []APIContainers{
		makeDockerContainer("container", "image"),
		otherContainer,
	}
	s.dockerClient.images = []APIImages{
		makeDockerImage("test"),
		makeDockerImage("other-image"),
	}
	err := updateImages(s.dockerClient)
	c.Assert(err, IsNil)
	c.Assert(imagesUsed, HasLen, 2)
	otherImage := imagesUsed["other-image"]

	handleDockerContainerID(s.dockerClient, "container")
	c.Assert(imagesUsed["otherImage"].ObjectTTL, Not(DeepEquals), otherImage.ObjectTTL)
}

func (s *CleanupSuite) TestMarksCache(c *C) {
	cacheContainer := makeDockerCache("1", humanize.MByte)
	s.dockerClient.containers = []APIContainers{
		cacheContainer,
	}

	err := updateContainers(s.dockerClient)
	c.Assert(err, IsNil)
	c.Assert(cachesUsed, HasLen, 1)

	cacheUsed := cachesUsed[cacheContainer.ID]

	handleDockerContainerID(s.dockerClient, cacheContainer.ID)
	c.Assert(cachesUsed[cacheContainer.ID].ObjectTTL, Not(DeepEquals), cacheUsed.ObjectTTL)
}

func (s *CleanupSuite) TestMarksImage(c *C) {
	testImage := makeDockerImage("test")
	testContainer := makeDockerContainer("test", "test")
	s.dockerClient.images = []APIImages{
		testImage,
	}
	s.dockerClient.containers = []APIContainers{
		testContainer,
	}

	err := updateImages(s.dockerClient)
	c.Assert(err, IsNil)
	c.Assert(imagesUsed, HasLen, 1)

	imageUsed := imagesUsed["test"]

	handleDockerContainerID(s.dockerClient, testContainer.ID)
	c.Assert(imagesUsed["test"].ObjectTTL, Not(DeepEquals), imageUsed.ObjectTTL)
}

func (s *CleanupSuite) TestCycleWithEnoughDiskSpace(c *C) {
	s.dockerClient.freeSpace = humanize.GByte
	s.dockerClient.freeFiles = 100000
	err := doCycle(s.dockerClient, humanize.MByte, humanize.GByte, 1000, 10000)
	c.Assert(err, IsNil)
}

func (s *CleanupSuite) TestCycleUnableToCleanup(c *C) {
	s.dockerClient.freeSpace = humanize.KByte
	err := doCycle(s.dockerClient, humanize.MByte, humanize.GByte, 1000, 10000)
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Matches, "no images or caches to delete")
}

func (s *CleanupSuite) TestFreeingSpaceAndNotRemovingContainersWithInternalImages(c *C) {
	s.dockerClient.freeSpace = humanize.GByte
	s.dockerClient.images = []APIImages{
		makeDockerImageWithSize("test", 600*humanize.MByte),
		makeDockerImageWithSize("gitlab/gitlab-runner:test", 500*humanize.MByte),
	}

	err := updateImages(s.dockerClient)
	c.Assert(err, IsNil)

	err = doFreeSpace(s.dockerClient, 2*humanize.GByte, 100000)
	c.Assert(err, NotNil)
	c.Assert(s.dockerClient.removedImages, HasLen, 1)
}

func (s *CleanupSuite) TestFreeingSpaceByRemovingImages(c *C) {
	s.dockerClient.freeSpace = humanize.GByte
	s.dockerClient.images = []APIImages{
		makeDockerImageWithSize("test", 600*humanize.MByte),
		makeDockerImageWithSize("test2", 500*humanize.MByte),
	}

	err := updateImages(s.dockerClient)
	c.Assert(err, IsNil)

	err = doFreeSpace(s.dockerClient, 2*humanize.GByte, 100000)
	c.Assert(err, IsNil)
	c.Assert(s.dockerClient.removedImages, HasLen, 2)
}

func (s *CleanupSuite) TestFreeingFilesByRemovingImages(c *C) {
	s.dockerClient.images = []APIImages{
		makeDockerImageWithSize("test", 40*humanize.MByte),
	}
	s.dockerClient.freeSpace = humanize.TByte
	s.dockerClient.freeFiles = 500

	err := updateImages(s.dockerClient)
	c.Assert(err, IsNil)

	err = doCycle(s.dockerClient, humanize.MByte, humanize.GByte, 1000, 10000)
	c.Assert(err, IsNil)
	c.Assert(s.dockerClient.removedImages, HasLen, 1)
}

func (s *CleanupSuite) TestFreeingSpaceByRemovingCaches(c *C) {
	s.dockerClient.freeSpace = humanize.GByte
	s.dockerClient.containers = []APIContainers{
		makeDockerCache("1", 600*humanize.MByte),
		makeDockerCache("2", 500*humanize.MByte),
	}

	err := updateContainers(s.dockerClient)
	c.Assert(err, IsNil)

	err = doFreeSpace(s.dockerClient, 2*humanize.GByte, 100000)
	c.Assert(err, IsNil)
	c.Assert(s.dockerClient.removedContainers, HasLen, 2)
}

func (s *CleanupSuite) TestFreeingSpaceAndIgnoringNonCacheContainers(c *C) {
	s.dockerClient.freeSpace = humanize.GByte
	s.dockerClient.containers = []APIContainers{
		makeDockerCache("1", 600*humanize.MByte),
		makeDockerContainer("test", "image"),
	}

	err := updateContainers(s.dockerClient)
	c.Assert(err, IsNil)

	err = doFreeSpace(s.dockerClient, 2*humanize.GByte, 100000)
	c.Assert(err, NotNil)
	c.Assert(s.dockerClient.removedContainers, HasLen, 1)
}
