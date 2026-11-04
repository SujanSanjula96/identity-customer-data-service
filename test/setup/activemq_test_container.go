/*
 * Copyright (c) 2026, WSO2 LLC. (http://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

package setup

import (
	"context"
	"fmt"
	"log"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	activemqImage     = "apache/activemq-classic:5.18.3"
	activemqUser      = "admin"
	activemqPassword  = "admin"
	activemqSTOMPPort = "61613/tcp"
)

// TestActiveMQ holds the running testcontainer and the STOMP connection
// address for the test ActiveMQ broker.
type TestActiveMQ struct {
	Container testcontainers.Container
	// Addr is the STOMP endpoint in "host:port" format.
	Addr     string
	Username string
	Password string
}

// SetupTestActiveMQ starts an Apache ActiveMQ Classic container and returns a
// TestActiveMQ whose Addr field points to the broker's STOMP port.
func SetupTestActiveMQ(ctx context.Context) (*TestActiveMQ, error) {
	req := testcontainers.ContainerRequest{
		Image:        activemqImage,
		ExposedPorts: []string{activemqSTOMPPort},
		// Wait until the STOMP connector is fully ready, not just until the
		// TCP port is open (which can happen before the protocol handler is
		// initialised, causing "connection reset by peer" errors).
		WaitingFor: wait.ForLog("Connector stomp started"),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start ActiveMQ container: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("failed to get ActiveMQ container host: %w", err)
	}

	port, err := container.MappedPort(ctx, "61613")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("failed to get ActiveMQ STOMP port: %w", err)
	}

	addr := fmt.Sprintf("%s:%s", host, port.Port())
	log.Printf("ActiveMQ container started – STOMP address: %s", addr)

	return &TestActiveMQ{
		Container: container,
		Addr:      addr,
		Username:  activemqUser,
		Password:  activemqPassword,
	}, nil
}
