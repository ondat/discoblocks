package diskinfo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/fatedier/frp/server"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var startPoll = sync.Once{}

var proxies = sync.Map{}

var proxyPollLog = logf.Log.WithName("pkg.diskinfo.ProxyPoll")

func getProxy(name, namespace string) (string, error) {
	startPoll.Do(func() {
		go func() {
			httpClient := http.Client{}

			for {
				func() {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()

					req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:8000/api/proxy/tcp", http.NoBody)
					if err != nil {
						panic(err)
					}

					resp, err := httpClient.Do(req)
					if err != nil {
						proxyPollLog.Error(err, "failed to call dashboard")
						return
					}
					defer func() {
						if err := resp.Body.Close(); err != nil {
							proxyPollLog.Error(err, "failed to close body")
						}
					}()

					content, err := io.ReadAll(resp.Body)
					if err != nil {
						proxyPollLog.Error(err, "failed to read body")
						return
					}

					proxyInfo := server.GetProxyInfoResp{}
					if err := json.Unmarshal(content, &proxyInfo); err != nil {
						proxyPollLog.Error(err, "failed to unmarshal content")
						return
					}

					proxyPollLog.Info("Registered proxies", "count", len(proxyInfo.Proxies))

					for i := range proxyInfo.Proxies {
						// u:rite's !!!!!!!!!!!!!!
						proxies.Store(proxyInfo.Proxies[i].Name, proxyInfo.Proxies[i])
					}
				}()

				const five = 5
				<-time.After(five * time.Second)
			}
		}()
	})

	rawProxy, ok := proxies.Load(fmt.Sprintf("%s-%s", namespace, name))
	if !ok {
		return "", errors.New("proxy not found")
	}

	proxy, ok := rawProxy.(*server.ProxyStatsInfo)
	if !ok {
		panic("wrong type in cache")
	}

	if proxy.Status != "online" {
		return "", fmt.Errorf("invalid client status: %s", proxy.Status)
	}

	conf, ok := proxy.Conf.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid config: %v", conf)
	}

	rawRemotePort, ok := conf["remote_port"]
	if !ok {
		return "", errors.New("missing remote_port")
	}

	remotePort, ok := rawRemotePort.(float64)
	if !ok {
		return "", fmt.Errorf("invalid port number: %d", rawRemotePort)
	}

	return fmt.Sprintf("127.0.0.1:%d", int(remotePort)), nil
}
