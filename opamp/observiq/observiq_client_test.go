// Copyright  observIQ, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package observiq

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	colmocks "github.com/observiq/observiq-otel-collector/collector/mocks"
	"github.com/observiq/observiq-otel-collector/internal/version"
	"github.com/observiq/observiq-otel-collector/opamp"
	"github.com/observiq/observiq-otel-collector/opamp/mocks"
	"github.com/open-telemetry/opamp-go/client/types"
	"github.com/open-telemetry/opamp-go/protobufs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewClient(t *testing.T) {
	secretKey := "136bdd08-2074-40b7-ac1c-6706ac24c4f2"
	testCases := []struct {
		desc        string
		config      opamp.Config
		expectedErr error
	}{
		{
			desc: "Bad URL Scheme",
			config: opamp.Config{
				Endpoint: "http://localhost:1234",
				AgentID:  "b24181a8-bc16-4ec1-b3af-ca6f7b669af8",
			},
			expectedErr: ErrUnsupportedURL,
		},
		{
			desc: "Invalid Endpoint",
			config: opamp.Config{
				Endpoint: "\t\t\t",
				AgentID:  "b24181a8-bc16-4ec1-b3af-ca6f7b669af8",
			},
			expectedErr: errors.New("net/url: invalid control character in URL"),
		},
		{
			desc: "Valid Config",
			config: opamp.Config{
				Endpoint:  "ws://localhost:1234",
				AgentID:   "b24181a8-bc16-4ec1-b3af-ca6f7b669af8",
				SecretKey: &secretKey,
			},
			expectedErr: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			testLogger := zap.NewNop()
			mockCollector := colmocks.NewMockCollector(t)

			tmpDir := t.TempDir()

			managerPath := filepath.Join(tmpDir, "manager.yaml")
			managerFile, err := os.Create(managerPath)
			assert.NoError(t, err)

			collectorPath := filepath.Join(tmpDir, "collector.yaml")
			collectorFile, err := os.Create(collectorPath)
			assert.NoError(t, err)

			loggerPath := filepath.Join(tmpDir, "logger.yaml")
			loggerFile, err := os.Create(loggerPath)
			assert.NoError(t, err)

			// We need to close the files specifically so windows can clean up the tmp dir
			defer func() {
				err := managerFile.Close()
				assert.NoError(t, err)
				err = collectorFile.Close()
				assert.NoError(t, err)
				err = loggerFile.Close()
				assert.NoError(t, err)
			}()

			args := &NewClientArgs{
				DefaultLogger:       testLogger,
				Config:              tc.config,
				Collector:           mockCollector,
				ManagerConfigPath:   managerPath,
				CollectorConfigPath: collectorPath,
				LoggerConfigPath:    loggerPath,
			}

			actual, err := NewClient(args)

			if tc.expectedErr != nil {
				assert.ErrorContains(t, err, tc.expectedErr.Error())
				assert.Nil(t, actual)
			} else {
				assert.NoError(t, err)

				observiqClient, ok := actual.(*Client)
				require.True(t, ok)

				// Do a shallow check on all fields to assert they exist and are equal to passed in params were possible
				assert.NotNil(t, observiqClient.opampClient)
				assert.NotNil(t, observiqClient.configManager)
				assert.Equal(t, testLogger.Named("opamp"), observiqClient.logger)
				assert.Equal(t, mockCollector, observiqClient.collector)
				assert.NotNil(t, observiqClient.ident)
				assert.Equal(t, observiqClient.currentConfig, tc.config)
			}

		})
	}
}

func TestClientConnect(t *testing.T) {
	secretKeyContents := "136bdd08-2074-40b7-ac1c-6706ac24c4f2"
	testCases := []struct {
		desc     string
		testFunc func(*testing.T)
	}{
		{
			desc: "SetAgentDescription fails",
			testFunc: func(*testing.T) {
				expectedErr := errors.New("oops")

				mockOpAmpClient := new(mocks.MockOpAMPClient)
				mockOpAmpClient.On("SetAgentDescription", mock.Anything).Return(expectedErr)

				c := &Client{
					opampClient:   mockOpAmpClient,
					logger:        zap.NewNop(),
					ident:         &identity{},
					configManager: nil,
					collector:     nil,
					currentConfig: opamp.Config{
						Endpoint:  "ws://localhost:1234",
						SecretKey: &secretKeyContents,
					},
				}

				err := c.Connect(context.Background())
				assert.ErrorIs(t, err, expectedErr)
			},
		},
		{
			desc: "Start fails",
			testFunc: func(*testing.T) {
				expectedErr := errors.New("oops")

				mockOpAmpClient := mocks.NewMockOpAMPClient(t)
				mockOpAmpClient.On("SetAgentDescription", mock.Anything).Return(nil)
				mockOpAmpClient.On("Start", mock.Anything, mock.Anything).Return(expectedErr)

				mockCollector := colmocks.NewMockCollector(t)
				mockCollector.On("Run", mock.Anything).Return(nil)

				c := &Client{
					opampClient:   mockOpAmpClient,
					logger:        zap.NewNop(),
					ident:         &identity{agentID: "a69dcef0-0261-4f4f-9ac0-a483af42a6ba"},
					configManager: nil,
					collector:     mockCollector,
					currentConfig: opamp.Config{
						Endpoint:  "ws://localhost:1234",
						SecretKey: &secretKeyContents,
					},
				}

				err := c.Connect(context.Background())
				assert.ErrorIs(t, err, expectedErr)
			},
		},
		{
			desc: "Collector fails to start",
			testFunc: func(*testing.T) {
				mockOpAmpClient := mocks.NewMockOpAMPClient(t)
				mockOpAmpClient.On("SetAgentDescription", mock.Anything).Return(nil)

				expectedErr := errors.New("oops")

				mockCollector := colmocks.NewMockCollector(t)
				mockCollector.On("Run", mock.Anything).Return(expectedErr)

				c := &Client{
					opampClient:   mockOpAmpClient,
					logger:        zap.NewNop(),
					ident:         &identity{agentID: "a69dcef0-0261-4f4f-9ac0-a483af42a6ba"},
					configManager: nil,
					collector:     mockCollector,
					currentConfig: opamp.Config{
						Endpoint:  "ws://localhost:1234",
						SecretKey: &secretKeyContents,
					},
				}

				err := c.Connect(context.Background())
				assert.ErrorIs(t, err, expectedErr)
			},
		},
		{
			desc: "Connect successful",
			testFunc: func(*testing.T) {
				mockOpAmpClient := mocks.NewMockOpAMPClient(t)
				mockOpAmpClient.On("SetAgentDescription", mock.Anything).Return(nil)

				mockCollector := colmocks.NewMockCollector(t)
				mockCollector.On("Run", mock.Anything).Return(nil)

				c := &Client{
					opampClient:   mockOpAmpClient,
					logger:        zap.NewNop(),
					ident:         &identity{agentID: "a69dcef0-0261-4f4f-9ac0-a483af42a6ba"},
					configManager: nil,
					collector:     mockCollector,
					currentConfig: opamp.Config{
						Endpoint:  "ws://localhost:1234",
						SecretKey: &secretKeyContents,
					},
				}

				expectedSettings := types.StartSettings{
					OpAMPServerURL: c.currentConfig.Endpoint,
					Header: http.Header{
						"Authorization": []string{fmt.Sprintf("Secret-Key %s", c.currentConfig.GetSecretKey())},
						"User-Agent":    []string{fmt.Sprintf("observiq-otel-collector/%s", version.Version())},
					},
					TLSConfig:   nil,
					InstanceUid: c.ident.agentID,
					Callbacks: types.CallbacksStruct{
						OnConnectFunc:          c.onConnectHandler,
						OnConnectFailedFunc:    c.onConnectFailedHandler,
						OnErrorFunc:            c.onErrorHandler,
						OnRemoteConfigFunc:     c.onRemoteConfigHandler,
						GetEffectiveConfigFunc: c.onGetEffectiveConfigHandler,
					},
				}
				mockOpAmpClient.On("Start", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
					settings := args.Get(1).(types.StartSettings)
					assert.Equal(t, expectedSettings.OpAMPServerURL, settings.OpAMPServerURL)
					assert.Equal(t, expectedSettings.Header, settings.Header)
					assert.Equal(t, expectedSettings.TLSConfig, settings.TLSConfig)
					assert.Equal(t, expectedSettings.InstanceUid, settings.InstanceUid)
					// assert is unable to compare function pointers
				})

				err := c.Connect(context.Background())
				assert.NoError(t, err)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, tc.testFunc)
	}
}

func TestClientDisconnect(t *testing.T) {
	ctx := context.Background()
	mockOpAmpClient := new(mocks.MockOpAMPClient)
	mockOpAmpClient.On("Stop", ctx).Return(nil)
	mockCollector := colmocks.NewMockCollector(t)
	mockCollector.On("Stop").Return()

	c := &Client{
		opampClient: mockOpAmpClient,
		collector:   mockCollector,
	}

	c.Disconnect(ctx)
	mockOpAmpClient.AssertExpectations(t)
}

func TestClient_onGetEffectiveConfigHandler(t *testing.T) {
	mockManager := new(mocks.MockConfigManager)

	c := &Client{
		logger:        zap.NewNop(),
		configManager: mockManager,
	}

	mockManager.On("ComposeEffectiveConfig").Return(&protobufs.EffectiveConfig{}, nil)

	c.onGetEffectiveConfigHandler(context.Background())
	mockManager.AssertExpectations(t)
}

func TestClient_onRemoteConfigHandler(t *testing.T) {
	testCases := []struct {
		desc     string
		testFunc func(*testing.T)
	}{
		{
			desc: "Config Changes return error",
			testFunc: func(*testing.T) {
				expectedErr := errors.New("oops")
				expectedChanged := false
				mockManager := new(mocks.MockConfigManager)
				mockManager.On("ApplyConfigChanges", mock.Anything).Return(&protobufs.EffectiveConfig{}, expectedChanged, expectedErr)

				c := &Client{
					configManager: mockManager,
					logger:        zap.NewNop(),
				}

				effCfg, changed, err := c.onRemoteConfigHandler(context.Background(), &protobufs.AgentRemoteConfig{})
				assert.Nil(t, effCfg)
				assert.Equal(t, expectedChanged, changed)
				assert.ErrorIs(t, err, expectedErr)
			},
		},
		{
			desc: "Config Changes occur",
			testFunc: func(*testing.T) {
				expectedEffCfg := &protobufs.EffectiveConfig{}
				mockManager := new(mocks.MockConfigManager)
				mockManager.On("ApplyConfigChanges", mock.Anything).Return(expectedEffCfg, true, nil)

				c := &Client{
					configManager: mockManager,
					logger:        zap.NewNop(),
				}

				effCfg, changed, err := c.onRemoteConfigHandler(context.Background(), &protobufs.AgentRemoteConfig{})
				assert.NoError(t, err)
				assert.Equal(t, expectedEffCfg, effCfg)
				assert.True(t, changed)
			},
		},
		{
			desc: "No Config Changes occur",
			testFunc: func(*testing.T) {
				expectedEffCfg := &protobufs.EffectiveConfig{}
				mockManager := new(mocks.MockConfigManager)
				mockManager.On("ApplyConfigChanges", mock.Anything).Return(expectedEffCfg, false, nil)

				c := &Client{
					configManager: mockManager,
					logger:        zap.NewNop(),
				}

				effCfg, changed, err := c.onRemoteConfigHandler(context.Background(), &protobufs.AgentRemoteConfig{})
				assert.NoError(t, err)
				assert.Equal(t, expectedEffCfg, effCfg)
				assert.False(t, changed)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, tc.testFunc)
	}
}