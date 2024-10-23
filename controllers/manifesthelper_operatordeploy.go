package controllers

import (
	"k8s.io/apimachinery/pkg/runtime"
	"open-cluster-management.io/addon-framework/pkg/addonfactory"
)

type manifestHelperOperatorDeploy struct {
	manifestHelperCommon
}

var _ manifestHelper = &manifestHelperOperatorDeploy{}

func (mh *manifestHelperOperatorDeploy) loadManifests(values addonfactory.Values,
) ([]runtime.Object, error) {
	return mh.loadManifestsFromFiles(manifestFilesOperatorDeploy, values)
}
