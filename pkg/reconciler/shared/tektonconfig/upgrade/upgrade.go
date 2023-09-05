/*
Copyright 2023 The Tekton Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package upgrade

import (
	"context"

	"github.com/tektoncd/operator/pkg/apis/operator/v1alpha1"
	"github.com/tektoncd/operator/pkg/client/clientset/versioned"
	"go.uber.org/zap"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"knative.dev/pkg/logging"
)

var (
	// pre upgrade functions
	preUpgradeFunctions = []upgradeFunc{
		upgradeChainProperties,      // upgrade #1: upgrade chain properties
		resetTektonConfigConditions, // upgrade #2: removes conditions from TektonConfig CR, clears outdated conditions
	}

	// post upgrade functions
	postUpgradeFunctions = []upgradeFunc{
		upgradeStorageVersion, // upgrade #1: performs storage version migration
	}
)

type upgradeFunc = func(ctx context.Context, logger *zap.SugaredLogger, k8sClient kubernetes.Interface, operatorClient versioned.Interface, restConfig *rest.Config) error

type Upgrade struct {
	logger          *zap.SugaredLogger
	operatorVersion string
	k8sClient       kubernetes.Interface
	operatorClient  versioned.Interface
	restConfig      *rest.Config
}

func New(operatorVersion string, k8sClient kubernetes.Interface, operatorClient versioned.Interface, restConfig *rest.Config) *Upgrade {
	return &Upgrade{
		k8sClient:       k8sClient,
		operatorClient:  operatorClient,
		operatorVersion: operatorVersion,
		restConfig:      restConfig,
	}
}

func (ug *Upgrade) RunPreUpgrade(ctx context.Context) error {
	return ug.executeUpgrade(ctx, preUpgradeFunctions, false)
}

func (ug *Upgrade) RunPostUpgrade(ctx context.Context) error {
	return ug.executeUpgrade(ctx, postUpgradeFunctions, true)
}

func (ug *Upgrade) executeUpgrade(ctx context.Context, upgradeFunctions []upgradeFunc, isPostUpgrade bool) error {
	// update logger
	ug.logger = logging.FromContext(ctx).Named("upgrade")

	// if upgrade not required return from here
	isUpgradeRequired, err := ug.isUpgradeRequired(ctx)
	if err != nil {
		return err
	}
	if !isUpgradeRequired {
		return nil
	}

	if isPostUpgrade {
		ug.logger.Debugw("executing post upgrade functions", "numberOfFunctions", len(upgradeFunctions))
	} else {
		ug.logger.Debugw("executing pre upgrade functions", "numberOfFunctions", len(upgradeFunctions))
	}

	// execute upgrade functions
	for _, _upgradeFunc := range upgradeFunctions {
		if err := _upgradeFunc(ctx, ug.logger, ug.k8sClient, ug.operatorClient, ug.restConfig); err != nil {
			ug.logger.Errorf("error on upgrade, error:%s", err.Error())
			return err
		}
	}
	if isPostUpgrade {
		ug.logger.Debug("completed post upgrade execution")
	} else {
		ug.logger.Debug("completed pre upgrade execution")
	}
	// update applied upgrade version
	if isPostUpgrade {
		return ug.updateAppliedUpgradeVersion(ctx)
	}
	return nil
}

func (ug *Upgrade) isUpgradeRequired(ctx context.Context) (bool, error) {
	tcCR, err := ug.operatorClient.OperatorV1alpha1().TektonConfigs().Get(ctx, v1alpha1.ConfigResourceName, metav1.GetOptions{})
	if err != nil {
		if apierrs.IsNotFound(err) {
			return false, nil
		}
		ug.logger.Errorw("error on getting TektonConfig CR", err)
		return false, err
	}

	_isUpgradeRequired := ug.operatorVersion != tcCR.Status.GetAppliedUpgradeVersion()
	return _isUpgradeRequired, nil
}

func (ug *Upgrade) updateAppliedUpgradeVersion(ctx context.Context) error {
	// update applied version into TektonConfig CR, under status
	_cr, err := ug.operatorClient.OperatorV1alpha1().TektonConfigs().Get(ctx, v1alpha1.ConfigResourceName, metav1.GetOptions{})
	if err != nil {
		ug.logger.Errorw("error on getting TektonConfig CR", err)
		return err
	}
	_cr.Status.SetAppliedUpgradeVersion(ug.operatorVersion)
	_, err = ug.operatorClient.OperatorV1alpha1().TektonConfigs().UpdateStatus(ctx, _cr, metav1.UpdateOptions{})
	if err != nil {
		ug.logger.Errorw("error on updating TektonConfig CR status", "version", ug.operatorVersion, err)
		return err
	}
	return nil
}