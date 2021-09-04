package app

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/arttor/helmify/pkg/config"
	"github.com/arttor/helmify/pkg/decoder"
	"github.com/arttor/helmify/pkg/helm"
	"github.com/arttor/helmify/pkg/processor/configmap"
	"github.com/arttor/helmify/pkg/processor/crd"
	"github.com/arttor/helmify/pkg/processor/deployment"
	"github.com/arttor/helmify/pkg/processor/rbac"
	"github.com/arttor/helmify/pkg/processor/secret"
	"github.com/arttor/helmify/pkg/processor/service"
	"github.com/arttor/helmify/pkg/processor/webhook"
	"github.com/sirupsen/logrus"
)

// Start - application entrypoint for processing input to a Helm chart.
func Start(input io.Reader, config config.Config) error {
	setLogLevel(config)
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-done
		logrus.Debug("Received termination, signaling shutdown")
		cancelFunc()
	}()
	objects := decoder.Decode(ctx.Done(), input)
	appContext := &Context{}
	appContext = appContext.WithConfig(config).WithProcessors(configmap.New(),
		crd.New(),
		deployment.New(),
		service.New(),
		rbac.ClusterRoleBinding(),
		rbac.Role(),
		rbac.RoleBinding(),
		rbac.ServiceAccount(),
		secret.New(),
		webhook.Issuer(),
		webhook.Certificate(),
		webhook.Webhook()).WithOutput(helm.NewOutput())

	for obj := range objects {
		appContext.Add(obj)
	}
	return appContext.CreateHelm(ctx.Done())
}

func setLogLevel(config config.Config) {
	logrus.SetLevel(logrus.ErrorLevel)
	if config.Verbose {
		logrus.SetLevel(logrus.InfoLevel)
	}
	if config.VeryVerbose {
		logrus.SetLevel(logrus.DebugLevel)
	}
}
