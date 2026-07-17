/*
Copyright 2026.

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

package controllers

import (
	"k8s.io/apimachinery/pkg/runtime"

	"open-cluster-management.io/addon-framework/pkg/addonfactory"
	"open-cluster-management.io/addon-framework/pkg/agent"
)

// Used when the VolSync install is disabled via annotation override
// (volsync-addon-deploy-type = disabled).
// This allows creation of the ManagedClusterAddon CR without actually deploying VolSync on the managed cluster
type manifestHelperNoOp struct {
	manifestHelperCommon
}

var _ manifestHelper = &manifestHelperNoOp{}

func (mh *manifestHelperNoOp) loadManifests() ([]runtime.Object, error) {
	// We will deploy only the namespace itself - this is so that the health check can be done on the namespace
	// and the ManagedClusterAddon CR can be set to available.  Nothing will actually be deployed into the namespace.
	objects, err := mh.loadManifestsFromFiles(manifestFilesNoOp, mh.getValuesForManifest())
	if err != nil {
		return nil, err
	}
	return objects, nil
}

func (mh *manifestHelperNoOp) subHealthCheck(fieldResults []agent.FieldResult) error {
	// Always return healthy for the no-op manifest helper, since we are not deploying anything
	return nil
}

func (mh *manifestHelperNoOp) getValuesForManifest() addonfactory.Values {
	return addonfactory.Values{
		"InstallNamespace": mh.getInstallNamespace(),
	}
}

func (mh *manifestHelperNoOp) getInstallNamespace() string {
	// This namespace should be different from our real namespace, to avoid issues with manifestworks when
	// enabling/disabling VolSync on a managed cluster
	// The no-op will still create this namespace, but nothing will be deployed into it.
	// This allows the healthchecker to check for the namespace
	return DefaultHelmInstallNamespace + "-disabled"
}
