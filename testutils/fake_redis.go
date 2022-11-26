package testsutils

import (
	"context"
	"fmt"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func GetFakeRedisURL(ctx context.Context) (redisURL string, teardown func(), err error) {
	var targetPort nat.Port = "6379"

	req := testcontainers.ContainerRequest{
		Image:        "redis",
		ExposedPorts: []string{fmt.Sprintf("%s/tcp", targetPort)},
		WaitingFor:   wait.ForListeningPort(targetPort).WithStartupTimeout(5 * time.Minute),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	teardown = func() { _ = container.Terminate(ctx) }
	if err != nil {
		return "", teardown, err
	}

	ip, err := container.Host(ctx)
	if err != nil {
		return "", teardown, err
	}

	port, err := container.MappedPort(ctx, targetPort)
	if err != nil {
		return "", teardown, err
	}

	url := fmt.Sprintf("redis://%s:%s/%d", ip, port.Port(), 0)
	return url, teardown, nil
}
