// Copyright 2020 Red Hat
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package pagerdutyintegration

import (
	"context"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	hiveapis "github.com/openshift/hive/pkg/apis"
	hivev1 "github.com/openshift/hive/pkg/apis/hive/v1"
	"github.com/openshift/pagerduty-operator/config"
	pagerdutyapis "github.com/openshift/pagerduty-operator/pkg/apis"
	pagerdutyv1alpha1 "github.com/openshift/pagerduty-operator/pkg/apis/pagerduty/v1alpha1"
	"github.com/openshift/pagerduty-operator/pkg/kube"
	pd "github.com/openshift/pagerduty-operator/pkg/pagerduty"
	mockpd "github.com/openshift/pagerduty-operator/pkg/pagerduty/mock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakekubeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	testPagerDutyIntegrationName = "testPagerDutyIntegration"
	testClusterName              = "testCluster"
	testNamespace                = "testNamespace"
	testIntegrationID            = "ABC123"
	testServiceID                = "DEF456"
	testAPIKey                   = "test-pd-api-key"
	testEscalationPolicy         = "test-escalation-policy"
	testResolveTimeout           = 300
	testAcknowledgeTimeout       = 300
	testOtherSyncSetPostfix      = "-something-else"
	testsecretReferencesName     = "pd-secret"
	testServicePrefix            = "test-service-prefix"
)

type SyncSetEntry struct {
	name                     string
	clusterDeploymentRefName string
	targetSecret             hivev1.SecretReference
}

type SecretEntry struct {
	name         string
	pagerdutyKey string
}

type mocks struct {
	fakeKubeClient client.Client
	mockCtrl       *gomock.Controller
	mockPDClient   *mockpd.MockClient
}

//rawToSecret takes a SyncSet resource and returns the decoded Secret it contains.
func rawToSecret(raw runtime.RawExtension) *corev1.Secret {
	decoder := scheme.Codecs.UniversalDecoder(corev1.SchemeGroupVersion)

	obj, _, err := decoder.Decode(raw.Raw, nil, nil)
	if err != nil {
		// okay, not everything in the syncset is necessarily a secret
		return nil
	}
	s, ok := obj.(*corev1.Secret)
	if ok {
		return s
	}

	return nil
}

func setupDefaultMocks(t *testing.T, localObjects []runtime.Object) *mocks {
	mocks := &mocks{
		fakeKubeClient: fakekubeclient.NewFakeClient(localObjects...),
		mockCtrl:       gomock.NewController(t),
	}

	mocks.mockPDClient = mockpd.NewMockClient(mocks.mockCtrl)

	return mocks
}

// testPDConfigSecret creates a fake secret containing pagerduty config details to use for testing.
func testPDConfigSecret() *corev1.Secret {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: config.OperatorNamespace,
			Name:      config.PagerDutyAPISecretName,
		},
		Data: map[string][]byte{
			config.PagerDutyAPISecretKey: []byte(testAPIKey),
		},
	}
	return s
}

// testPDConfigMap returns a fake configmap for a deployed cluster for testing.
func testPDConfigMap() *corev1.ConfigMap {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      config.Name(testServicePrefix, testClusterName, config.ConfigMapSuffix),
		},
		Data: map[string]string{
			"INTEGRATION_ID": testIntegrationID,
			"SERVICE_ID":     testServiceID,
		},
	}
	return cm
}

// testSecret returns a Secret that will go in the SyncSet for a deployed cluster to use in testing.
func testSecret() *corev1.Secret {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testServicePrefix + "-" + testClusterName + "-" + testsecretReferencesName,
			Namespace: testNamespace,
		},
		Data: map[string][]byte{
			config.PagerDutySecretKey: []byte(testIntegrationID),
		},
	}
	return s
}

// testSyncSet returns a SyncSet for an existing testClusterDeployment to use in testing.
func testSyncSet() *hivev1.SyncSet {
	secretName := config.Name(testServicePrefix, testClusterName, config.SecretSuffix)
	secret := kube.GeneratePdSecret(testNamespace, secretName, testIntegrationID)
	pdi := testPagerDutyIntegration()
	ss := kube.GenerateSyncSet(testNamespace, testClusterName, secret, pdi)
	return ss
}

// testOtherSyncSet returns a SyncSet that is not for PD for an existing testClusterDeployment to use in testing.
func testOtherSyncSet() *hivev1.SyncSet {
	return &hivev1.SyncSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testClusterName + testOtherSyncSetPostfix,
			Namespace: testNamespace,
		},
		Spec: hivev1.SyncSetSpec{
			ClusterDeploymentRefs: []corev1.LocalObjectReference{
				{
					Name: testClusterName,
				},
			},
		},
	}
}

func testPagerDutyIntegration() *pagerdutyv1alpha1.PagerDutyIntegration {
	return &pagerdutyv1alpha1.PagerDutyIntegration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testPagerDutyIntegrationName,
			Namespace: config.OperatorNamespace,
		},
		Spec: pagerdutyv1alpha1.PagerDutyIntegrationSpec{
			AcknowledgeTimeout: testAcknowledgeTimeout,
			ResolveTimeout:     testResolveTimeout,
			EscalationPolicy:   testEscalationPolicy,
			ServicePrefix:      testServicePrefix,
			ClusterDeploymentSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{config.ClusterDeploymentManagedLabel: "true"},
			},
			PagerdutyApiKeySecretRef: corev1.SecretReference{
				Name:      config.PagerDutyAPISecretName,
				Namespace: config.OperatorNamespace,
			},
			TargetSecretRef: corev1.SecretReference{
				Name:      config.Name(testServicePrefix, testClusterName, config.SecretSuffix),
				Namespace: testNamespace,
			},
		},
	}
}

// testClusterDeployment returns a fake ClusterDeployment for an installed cluster to use in testing.
func testClusterDeployment() *hivev1.ClusterDeployment {
	labelMap := map[string]string{config.ClusterDeploymentManagedLabel: "true"}
	cd := hivev1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testClusterName,
			Namespace: testNamespace,
			Labels:    labelMap,
		},
		Spec: hivev1.ClusterDeploymentSpec{
			ClusterName: testClusterName,
		},
	}
	cd.Spec.Installed = true

	return &cd
}

// deletedClusterDeployment returns a fake deleted ClusterDeployment to use in testing.
func deletedClusterDeployment(pdiName string) *hivev1.ClusterDeployment {
	cd := testClusterDeployment()
	now := metav1.Now()
	cd.DeletionTimestamp = &now
	cd.SetFinalizers([]string{"pd.managed.openshift.io/" + pdiName})

	return cd
}

// unmanagedClusterDeployment returns a fake ClusterDeployment labelled with "api.openshift.com/managed = False" to use in testing.
func unmanagedClusterDeployment() *hivev1.ClusterDeployment {
	labelMap := map[string]string{config.ClusterDeploymentManagedLabel: "false"}
	cd := testClusterDeployment()
	cd.SetLabels(labelMap)
	return cd
}

// unlabelledClusterDeployment returns a fake ClusterDeployment with no "api.openshift.com/managed" label present to use in testing.
func unlabelledClusterDeployment() *hivev1.ClusterDeployment {
	cd := testClusterDeployment()
	cd.SetLabels(map[string]string{})
	return cd
}

// uninstalledClusterDeployment returns a ClusterDeployment with Spec.Installed == false to use in testing.
func uninstalledClusterDeployment() *hivev1.ClusterDeployment {
	cd := testClusterDeployment()
	cd.Spec.Installed = false

	return cd
}

func TestReconcilePagerDutyIntegration(t *testing.T) {
	hiveapis.AddToScheme(scheme.Scheme)
	pagerdutyapis.AddToScheme(scheme.Scheme)
	tests := []struct {
		name             string
		localObjects     []runtime.Object
		expectedSyncSets *SyncSetEntry
		expectedSecrets  *SecretEntry
		verifySyncSets   func(client.Client, *SyncSetEntry) bool
		verifySecrets    func(client.Client, *SecretEntry) bool
		setupPDMock      func(*mockpd.MockClientMockRecorder)
	}{
		{
			name: "Test Creating",
			localObjects: []runtime.Object{
				testClusterDeployment(),
				testPDConfigSecret(),
				testPagerDutyIntegration(),
			},
			expectedSyncSets: &SyncSetEntry{
				name:                     config.Name(testServicePrefix, testClusterName, config.SecretSuffix),
				clusterDeploymentRefName: testClusterName,
				targetSecret: hivev1.SecretReference{
					Name:      testPagerDutyIntegration().Spec.TargetSecretRef.Name,
					Namespace: testPagerDutyIntegration().Spec.TargetSecretRef.Namespace,
				},
			},
			expectedSecrets: &SecretEntry{
				name:         config.Name(testServicePrefix, testClusterName, config.SecretSuffix),
				pagerdutyKey: testIntegrationID,
			},
			verifySyncSets: verifySyncSetExists,
			verifySecrets:  verifySecretExists,
			setupPDMock: func(r *mockpd.MockClientMockRecorder) {
				r.CreateService(gomock.Any()).Return(testIntegrationID, nil).Times(1)
				r.GetIntegrationKey(gomock.Any()).Return(testIntegrationID, nil).Times(1)
				r.DeleteService(gomock.Any()).Return(nil).Times(0)
			},
		},
		{
			name: "Test Deleting",
			localObjects: []runtime.Object{
				deletedClusterDeployment(testPagerDutyIntegrationName),
				testPDConfigSecret(),
				testPDConfigMap(),
				testPagerDutyIntegration(),
			},
			expectedSyncSets: &SyncSetEntry{},
			expectedSecrets:  &SecretEntry{},
			verifySyncSets:   verifyNoSyncSetExists,
			verifySecrets:    verifyNoSecretExists,
			setupPDMock: func(r *mockpd.MockClientMockRecorder) {
				r.CreateService(gomock.Any()).Return(testIntegrationID, nil).Times(0)
				r.GetIntegrationKey(gomock.Any()).Return(testIntegrationID, nil).Times(0)
				r.DeleteService(gomock.Any()).Return(nil).Times(1)
			},
		},
		{
			name: "Test Deleting with missing ConfigMap",
			localObjects: []runtime.Object{
				deletedClusterDeployment(testPagerDutyIntegrationName),
				testPDConfigSecret(),
				testPagerDutyIntegration(),
			},
			expectedSyncSets: &SyncSetEntry{},
			expectedSecrets:  &SecretEntry{},
			verifySyncSets:   verifyNoSyncSetExists,
			verifySecrets:    verifyNoSecretExists,
			setupPDMock: func(r *mockpd.MockClientMockRecorder) {
				r.CreateService(gomock.Any()).Return(testIntegrationID, nil).Times(0)
				r.GetIntegrationKey(gomock.Any()).Return(testIntegrationID, nil).Times(0)
				r.DeleteService(gomock.Any()).Return(nil).Times(0)
			},
		},
		{
			name: "Test Uninstalled Cluster",
			localObjects: []runtime.Object{
				uninstalledClusterDeployment(),
				testPagerDutyIntegration(),
				testPDConfigSecret(),
			},
			expectedSyncSets: &SyncSetEntry{},
			expectedSecrets:  &SecretEntry{},
			verifySyncSets:   verifyNoSyncSetExists,
			verifySecrets:    verifyNoSecretExists,
			setupPDMock: func(r *mockpd.MockClientMockRecorder) {
				r.CreateService(gomock.Any()).Return(testIntegrationID, nil).Times(0)
				r.GetIntegrationKey(gomock.Any()).Return(testIntegrationID, nil).Times(0)
				r.DeleteService(gomock.Any()).Return(nil).Times(0)
			},
		},
		{
			name: "Test Updating",
			localObjects: []runtime.Object{
				testClusterDeployment(),
				testSecret(),
				testSyncSet(),
				testPDConfigMap(),
				testPDConfigSecret(),
				testPagerDutyIntegration(),
			},
			expectedSyncSets: &SyncSetEntry{
				name:                     config.Name(testServicePrefix, testClusterName, config.SecretSuffix),
				clusterDeploymentRefName: testClusterName,
				targetSecret: hivev1.SecretReference{
					Name:      testPagerDutyIntegration().Spec.TargetSecretRef.Name,
					Namespace: testPagerDutyIntegration().Spec.TargetSecretRef.Namespace,
				},
			},
			expectedSecrets: &SecretEntry{
				name:         config.Name(testServicePrefix, testClusterName, config.SecretSuffix),
				pagerdutyKey: testIntegrationID,
			},
			verifySyncSets: verifySyncSetExists,
			verifySecrets:  verifySecretExists,
			setupPDMock: func(r *mockpd.MockClientMockRecorder) {
				r.CreateService(gomock.Any()).Return(testIntegrationID, nil).Times(0)
				r.GetIntegrationKey(gomock.Any()).Return(testIntegrationID, nil).Times(0)
				r.DeleteService(gomock.Any()).Return(nil).Times(0)
			},
		},
		{
			name: "Test Creating (unmanaged with label)",
			localObjects: []runtime.Object{
				unmanagedClusterDeployment(),
				testPDConfigSecret(),
				testPagerDutyIntegration(),
			},
			expectedSyncSets: &SyncSetEntry{},
			expectedSecrets:  &SecretEntry{},
			verifySyncSets:   verifyNoSyncSetExists,
			verifySecrets:    verifyNoSecretExists,
			setupPDMock: func(r *mockpd.MockClientMockRecorder) {
				r.CreateService(gomock.Any()).Return(testIntegrationID, nil).Times(0)
				r.GetIntegrationKey(gomock.Any()).Return(testIntegrationID, nil).Times(0)
				r.DeleteService(gomock.Any()).Return(nil).Times(0)
			},
		},
		{
			name: "Test Creating (unmanaged without label)",
			localObjects: []runtime.Object{
				unlabelledClusterDeployment(),
				testPagerDutyIntegration(),
				testPDConfigSecret(),
			},
			expectedSyncSets: &SyncSetEntry{},
			expectedSecrets:  &SecretEntry{},
			verifySyncSets:   verifyNoSyncSetExists,
			verifySecrets:    verifyNoSecretExists,
			setupPDMock: func(r *mockpd.MockClientMockRecorder) {
				r.CreateService(gomock.Any()).Return(testIntegrationID, nil).Times(0)
				r.GetIntegrationKey(gomock.Any()).Return(testIntegrationID, nil).Times(0)
				r.DeleteService(gomock.Any()).Return(nil).Times(0)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			mocks := setupDefaultMocks(t, test.localObjects)
			test.setupPDMock(mocks.mockPDClient.EXPECT())

			defer mocks.mockCtrl.Finish()

			rpdi := &ReconcilePagerDutyIntegration{
				client:   mocks.fakeKubeClient,
				scheme:   scheme.Scheme,
				pdclient: func(s1 string, s2 string) pd.Client { return mocks.mockPDClient },
			}

			// 1st run sets finalizer
			_, err1 := rpdi.Reconcile(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPagerDutyIntegrationName,
					Namespace: config.OperatorNamespace,
				},
			})

			// 2nd run does the initial work
			_, err2 := rpdi.Reconcile(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPagerDutyIntegrationName,
					Namespace: config.OperatorNamespace,
				},
			})

			// 3rd run should be a noop, we need to confirm
			_, err3 := rpdi.Reconcile(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPagerDutyIntegrationName,
					Namespace: config.OperatorNamespace,
				},
			})

			// Assert
			assert.NoError(t, err1, "Unexpected Error with Reconcile (1 of 3)")
			assert.NoError(t, err2, "Unexpected Error with Reconcile (2 of 3)")
			assert.NoError(t, err3, "Unexpected Error with Reconcile (3 of 3)")
			assert.True(t, test.verifySyncSets(mocks.fakeKubeClient, test.expectedSyncSets), "verifySyncSets: "+test.name)
			assert.True(t, test.verifySecrets(mocks.fakeKubeClient, test.expectedSecrets), "verifySecrets: "+test.name)
		})
	}
}

//TestDeleteSecret tests that the reconcile process when the pd-secret is being deleted
func TestDeleteSecret(t *testing.T) {
	t.Run("Test Delete Secret", func(t *testing.T) {
		// Arrange
		mocks := setupDefaultMocks(t, []runtime.Object{
			testClusterDeployment(),
			testPDConfigSecret(),
			testPagerDutyIntegration(),
		})

		expectedSyncSets := &SyncSetEntry{
			name:                     config.Name(testServicePrefix, testClusterName, config.SecretSuffix),
			clusterDeploymentRefName: testClusterName,
		}

		expectedSecrets := &SecretEntry{
			name:         config.Name(testServicePrefix, testClusterName, config.SecretSuffix),
			pagerdutyKey: testIntegrationID,
		}

		setupPDMock :=
			func(r *mockpd.MockClientMockRecorder) {
				r.CreateService(gomock.Any()).Return(testIntegrationID, nil).Times(1)
				r.GetIntegrationKey(gomock.Any()).Return(testIntegrationID, nil).Times(2)
				r.DeleteService(gomock.Any()).Return(nil).Times(0)
			}

		setupPDMock(mocks.mockPDClient.EXPECT())

		defer mocks.mockCtrl.Finish()

		rpdi := &ReconcilePagerDutyIntegration{
			client:   mocks.fakeKubeClient,
			scheme:   scheme.Scheme,
			pdclient: func(s1 string, s2 string) pd.Client { return mocks.mockPDClient },
		}

		// Act (create) [2x as first exits early after setting finalizer]
		_, err := rpdi.Reconcile(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      testPagerDutyIntegrationName,
				Namespace: config.OperatorNamespace,
			},
		})
		_, err = rpdi.Reconcile(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      testPagerDutyIntegrationName,
				Namespace: config.OperatorNamespace,
			},
		})

		// Remove the secret which is referred by the syncset
		secret := &corev1.Secret{}
		err = mocks.fakeKubeClient.Get(context.TODO(), types.NamespacedName{
			Namespace: testNamespace,
			Name:      config.Name(testServicePrefix, testClusterName, config.SecretSuffix),
		}, secret)
		err = mocks.fakeKubeClient.Delete(context.TODO(), secret)

		// Act (reconcile again)
		_, err = rpdi.Reconcile(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      testPagerDutyIntegrationName,
				Namespace: config.OperatorNamespace,
			},
		})

		// Assert
		assert.NoError(t, err, "Unexpected Error")
		assert.True(t, verifySyncSetExists(mocks.fakeKubeClient, expectedSyncSets))
		assert.True(t, verifySecretExists(mocks.fakeKubeClient, expectedSecrets))
	})
}

// testPDConfigMap returns a fake configmap for a deployed cluster for testing.
func testLegacyPDConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      testClusterName + config.ConfigMapSuffix,
		},
		Data: map[string]string{
			"INTEGRATION_ID": testIntegrationID,
			"SERVICE_ID":     testServiceID,
		},
	}
}

// testSecret returns a Secret that will go in the SyncSet for a deployed cluster to use in testing.
func testLegacySecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pd-secret",
			Namespace: testNamespace,
		},
		Data: map[string][]byte{
			config.PagerDutySecretKey: []byte(testIntegrationID),
		},
	}
}

// testSyncSet returns a SyncSet for an existing testClusterDeployment to use in testing.
func testLegacySyncSet() *hivev1.SyncSet {
	return &hivev1.SyncSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testClusterName + "-pd-sync",
			Namespace: testNamespace,
		},
		Spec: hivev1.SyncSetSpec{
			ClusterDeploymentRefs: []corev1.LocalObjectReference{{
				Name: testClusterName,
			}},
			SyncSetCommonSpec: hivev1.SyncSetCommonSpec{
				ResourceApplyMode: "Sync",
				Secrets: []hivev1.SecretMapping{
					{
						SourceRef: hivev1.SecretReference{
							Namespace: testNamespace,
							Name:      "pd-secret",
						},
						TargetRef: hivev1.SecretReference{
							Namespace: "openshift-monitoring",
							Name:      "pagerduty-api-key",
						},
					},
				},
			},
		},
	}
}

// verifySyncSetExists verifies that a SyncSet exists that matches the supplied expected SyncSetEntry.
func verifySyncSetExists(c client.Client, expected *SyncSetEntry) bool {
	ss := hivev1.SyncSet{}
	err := c.Get(context.TODO(),
		types.NamespacedName{Name: expected.name, Namespace: testNamespace},
		&ss)
	if err != nil {
		return false
	}

	if expected.name != ss.Name {
		return false
	}

	if expected.clusterDeploymentRefName != ss.Spec.ClusterDeploymentRefs[0].Name {
		return false
	}
	secretReferences := ss.Spec.SyncSetCommonSpec.Secrets[0].SourceRef.Name
	if secretReferences == "" {
		return false
	}
	return string(secretReferences) == expected.name
}

// verifyNoSyncSetExists verifies that there is no SyncSet present that matches the supplied expected SyncSetEntry.
func verifyNoSyncSetExists(c client.Client, expected *SyncSetEntry) bool {
	ssList := &hivev1.SyncSetList{}
	opts := client.ListOptions{Namespace: testNamespace}
	err := c.List(context.TODO(), ssList, &opts)

	if err != nil {
		if errors.IsNotFound(err) {
			// no syncsets are defined, this is OK
			return true
		}
	}

	for _, ss := range ssList.Items {
		if ss.Name != testClusterName+testOtherSyncSetPostfix {
			// too bad, found a syncset associated with this operator
			return false
		}
	}

	// if we got here, it's good.  list was empty or everything passed
	return true
}

func verifyNoConfigMapExists(c client.Client) bool {
	cmList := &corev1.ConfigMapList{}
	opts := client.ListOptions{Namespace: testNamespace}
	err := c.List(context.TODO(), cmList, &opts)

	if err != nil {
		if errors.IsNotFound(err) {
			// no configmaps are defined, this is OK
			return true
		}
	}

	for _, cm := range cmList.Items {
		if strings.HasSuffix(cm.Name, config.ConfigMapSuffix) {
			// too bad, found a configmap associated with this operator
			return false
		}
	}

	// if we got here, it's good.  list was empty or everything passed
	return true
}

// verifySecretExists verifies that the secret which referenced by the SyncSet exists in the test namespace
func verifySecretExists(c client.Client, expected *SecretEntry) bool {
	secret := corev1.Secret{}
	err := c.Get(context.TODO(),
		types.NamespacedName{Name: expected.name, Namespace: testNamespace},
		&secret)

	if err != nil {
		return false
	}

	if expected.name != secret.Name {
		return false
	}

	if expected.pagerdutyKey != string(secret.Data["PAGERDUTY_KEY"]) {
		return false
	}

	return true
}

// verifyNoSecretExists verifies that the secret which referred by SyncSet does not exist
func verifyNoSecretExists(c client.Client, expected *SecretEntry) bool {
	secretList := &corev1.SecretList{}
	opts := client.ListOptions{Namespace: testNamespace}
	err := c.List(context.TODO(), secretList, &opts)

	if err != nil {
		if errors.IsNotFound(err) {
			return true
		}
	}

	for _, secret := range secretList.Items {
		if secret.Name == testsecretReferencesName {
			return false
		}
	}

	return true
}
