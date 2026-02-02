package configuration

import (
	"fmt"

	"k8s.io/klog/v2"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/loglevel"

	i "github.com/openshift/cluster-version-operator/pkg/internal"
)

func applyLogLevel(level operatorv1.LogLevel) error {
	currentLogLevel, notFound := loglevel.GetLogLevel()
	if notFound {
		klog.Warningf("The current log level could not be found; an attempt to set the log level to the desired level will be made")
	}

	if !notFound && currentLogLevel == level {
		klog.V(i.Debug).Infof("No need to update the current CVO log level '%s'; it is already set to the desired value", currentLogLevel)
	} else {
		if err := loglevel.SetLogLevel(level); err != nil {
			return fmt.Errorf("failed to set the log level to %q: %w", level, err)
		}

		// E2E testing will be checking for existence or absence of these logs
		switch level {
		case operatorv1.Normal:
			klog.V(i.Normal).Infof("Successfully updated the log level from '%s' to 'Normal'", currentLogLevel)
		case operatorv1.Debug:
			klog.V(i.Debug).Infof("Successfully updated the log level from '%s' to 'Debug'", currentLogLevel)
		case operatorv1.Trace:
			klog.V(i.Trace).Infof("Successfully updated the log level from '%s' to 'Trace'", currentLogLevel)
		case operatorv1.TraceAll:
			klog.V(i.TraceAll).Infof("Successfully updated the log level from '%s' to 'TraceAll'", currentLogLevel)
		default:
			klog.Errorf("The CVO logging level has unexpected value '%s'", level)
		}
	}
	return nil
}
