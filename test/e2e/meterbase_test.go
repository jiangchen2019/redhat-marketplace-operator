// Copyright 2020 IBM Corp.
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

// +build e2e

package e2e

import (
	goctx "context"
	"fmt"
	"testing"

	"github.com/redhat-marketplace/redhat-marketplace-operator/pkg/apis"
	operator "github.com/redhat-marketplace/redhat-marketplace-operator/pkg/apis/marketplace/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"

	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMeterbase(t *testing.T) {
	t.Skip("skipping test in short mode.")

	meterbaseConfigList := &operator.MeterBaseList{}
	err := framework.AddToFrameworkScheme(apis.AddToScheme, meterbaseConfigList)
	if err != nil {
		t.Fatalf("failed to add custom resource scheme to framework: %v", err)
	}
	// run subtests
	t.Run("meterbase-group", func(t *testing.T) {
		t.Run("Cluster", MeterbaseOperatorCluster)
		t.Run("Cluster2", MeterbaseOperatorCluster)
	})
}

// meterbaseStatefulSetTest ensures the deployment and healing of a statefulset
func meterbaseScaleTest(t *testing.T, f *framework.Framework, ctx *framework.TestCtx) error {
	namespace, err := ctx.GetNamespace()
	if err != nil {
		return fmt.Errorf("could not get namespace: %v", err)
	}
	// create meterbase custom resource
	exampleMeterBase := &operator.MeterBase{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "meterbase",
			Namespace: namespace,
		},
		Spec: operator.MeterBaseSpec{
			Enabled: true,
			Prometheus: &operator.PrometheusSpec{
				Storage: operator.StorageSpec{
					Size: resource.MustParse("2Gi"),
				},
			},
		},
	}
	// use TestCtx's create helper to create the object and add a cleanup function for the new object
	err = f.Client.Create(goctx.TODO(), exampleMeterBase, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	if err != nil {
		return err
	}
	// wait for meterbase to reach 1 replicas
	err = waitForStatefulSet(t, f.KubeClient, namespace, "meterbase", 1, retryInterval, timeout)
	if err != nil {
		return err
	}

	err = f.Client.Get(goctx.TODO(), types.NamespacedName{Name: "example-meterbase", Namespace: namespace}, exampleMeterBase)
	if err != nil {
		return err
	}

	return nil
}

func MeterbaseOperatorCluster(t *testing.T) {
	t.Parallel()
	ctx := framework.NewTestCtx(t)
	defer ctx.Cleanup()
	err := ctx.InitializeClusterResources(&framework.CleanupOptions{
		TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	if err != nil {
		t.Fatalf("failed to initialize cluster resources: %v", err)
	}
	t.Log("Initialized cluster resources")
	namespace, err := ctx.GetNamespace()
	if err != nil {
		t.Fatal(err)
	}
	// get global framework variables
	f := framework.Global
	// wait for meterbase-operator to be ready
	err = e2eutil.WaitForOperatorDeployment(t, f.KubeClient, namespace, "redhat-marketplace-operator", 1, retryInterval, timeout)
	if err != nil {
		t.Fatal(err)
	}

	if err = meterbaseScaleTest(t, f, ctx); err != nil {
		t.Fatal(err)
	}
}
