package api

import (
	"crypto/tls"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/coreos/go-etcd/etcd"
	"github.com/denverdino/commander/api/filter"
	"github.com/denverdino/commander/registry"

	"net"
	"net/http"
	"strings"
)

const DefaultDockerPort = ":2375"

func newListener(proto, addr string, tlsConfig *tls.Config) (net.Listener, error) {
	l, err := net.Listen(proto, addr)
	if err != nil {
		if strings.Contains(err.Error(), "address already in use") && strings.Contains(addr, DefaultDockerPort) {
			return nil, fmt.Errorf("%s: is Docker already running on this machine? Try using a different port", err)
		}
		return nil, err
	}
	if tlsConfig != nil {
		tlsConfig.NextProtos = []string{"http/1.1"}
		l = tls.NewListener(l, tlsConfig)
	}
	return l, nil
}

func ListenAndServe(addr string, hosts []string, version string, enableCors bool, etcdURL string, tlsConfig *tls.Config) error {
	etcdClient := etcd.NewClient([]string{etcdURL})
	context := &filter.Context{
		Addr:       addr,
		Version:    version,
		EtcdClient: etcdClient,
		TLSConfig:  tlsConfig,
	}
	r := createRouter(context)

	go registry.WatchServiceChange(etcdClient)

	interceptor := NewInterceptor(context, r).addFilterByName("log").addFilterByName("cors")

	chErrors := make(chan error, len(hosts))

	for _, host := range hosts {
		protoAddrParts := strings.SplitN(host, "://", 2)
		if len(protoAddrParts) == 1 {
			protoAddrParts = append([]string{"tcp"}, protoAddrParts...)
		}

		go func() {
			log.WithFields(log.Fields{"proto": protoAddrParts[0], "addr": protoAddrParts[1]}).Info("Listening for HTTP")

			var (
				l      net.Listener
				err    error
				server = &http.Server{
					Addr:    protoAddrParts[1],
					Handler: interceptor.GetHandler(),
				}
			)

			switch protoAddrParts[0] {
			case "unix":
				l, err = newUnixListener(protoAddrParts[1], tlsConfig)
			case "tcp":
				l, err = newListener("tcp", protoAddrParts[1], tlsConfig)
			default:
				err = fmt.Errorf("unsupported protocol: %q", protoAddrParts[0])
			}
			if err != nil {
				chErrors <- err
			} else {
				chErrors <- server.Serve(l)
			}

		}()
	}

	for i := 0; i < len(hosts); i++ {
		err := <-chErrors
		if err != nil {
			return err
		}
	}
	return nil
}
