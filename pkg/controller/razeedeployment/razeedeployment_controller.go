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

package razeedeployment

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/banzaicloud/k8s-objectmatcher/patch"
	marketplacev1alpha1 "github.com/redhat-marketplace/redhat-marketplace-operator/pkg/apis/marketplace/v1alpha1"
	"github.com/redhat-marketplace/redhat-marketplace-operator/pkg/utils"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	appsv1 "k8s.io/api/apps/v1"
	batch "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var (
	RAZEE_WATCH_KEEPER_LABELS = map[string]string{"razee/watch-resource": "lite"}
	log                       = logf.Log.WithName("controller_razeedeployment")
	razeeFlagSet              *pflag.FlagSet
	RELATED_IMAGE_RAZEE_JOB = "RELATED_IMAGE_RAZEE_JOB"
)

func init() {
	razeeFlagSet = pflag.NewFlagSet("razee", pflag.ExitOnError)
	razeeFlagSet.String("razee-job-image", utils.Getenv(RELATED_IMAGE_RAZEE_JOB, utils.DEFAULT_RAZEE_JOB_IMAGE), "image for the razee job")
}

func FlagSet() *pflag.FlagSet {
	return razeeFlagSet
}

// Add creates a new RazeeDeployment Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	razeeOpts := &RazeeOpts{
		RazeeJobImage: viper.GetString("razee-job-image"),
	}

	return &ReconcileRazeeDeployment{client: mgr.GetClient(), scheme: mgr.GetScheme(), opts: razeeOpts}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("razeedeployment-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource RazeeDeployment
	err = c.Watch(&source.Kind{Type: &marketplacev1alpha1.RazeeDeployment{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	err = c.Watch(&source.Kind{Type: &batch.Job{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &marketplacev1alpha1.RazeeDeployment{},
	})
	if err != nil {
		return err
	}

	err = c.Watch(&source.Kind{Type: &corev1.Secret{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &marketplacev1alpha1.RazeeDeployment{},
	})
	if err != nil {
		return err
	}

	return nil

}

// blank assignment to verify that ReconcileRazeeDeployment implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileRazeeDeployment{}

// ReconcileRazeeDeployment reconciles a RazeeDeployment object
type ReconcileRazeeDeployment struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
	opts   *RazeeOpts
}

type RazeeOpts struct {
	RazeeJobImage string
	ClusterUUID   string
}

// Reconcile reads that state of the cluster for a RazeeDeployment object and makes changes based on the state read
// and what is in the RazeeDeployment.Spec
func (r *ReconcileRazeeDeployment) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling RazeeDeployment")
	reqLogger.Info("Beginning of RazeeDeploy Instance reconciler")
	// Fetch the RazeeDeployment instance
	instance := &marketplacev1alpha1.RazeeDeployment{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			reqLogger.Error(err, "Failed to find RazeeDeployment instance")
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// if not enabled then exit
	if !instance.Spec.Enabled {
		reqLogger.Info("Razee not enabled")
		return reconcile.Result{}, nil
	}

	// Adding a finalizer to this CR
	if !utils.Contains(instance.GetFinalizers(), utils.RAZEE_DEPLOYMENT_FINALIZER) {
		if err := r.addFinalizer(instance, request.Namespace); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Check if the RazeeDeployment instance is being marked for deletion
	isMarkedForDeletion := instance.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		if utils.Contains(instance.GetFinalizers(), utils.RAZEE_DEPLOYMENT_FINALIZER) {
			//Run finalization logic for the RAZEE_DEPLOYMENT_FINALIZER.
			//If it fails, don't remove the finalizer so we can retry during the next reconcile
			return r.partialUninstall(instance)
		}
		return reconcile.Result{}, nil
	}

	if instance.Spec.TargetNamespace == nil {
		if instance.Status.RazeeJobInstall != nil {
			instance.Spec.TargetNamespace = &instance.Status.RazeeJobInstall.RazeeNamespace
		} else {
			instance.Spec.TargetNamespace = &instance.Namespace
		}
		err := r.client.Update(context.TODO(), instance)
		if err != nil {
			return reconcile.Result{}, err
		}

		reqLogger.Info("set target namespace to", "namespace", instance.Spec.TargetNamespace)
		return reconcile.Result{}, nil
	}

	/******************************************************************************
	PROCEED WITH CREATING RAZEE PREREQUISITES?
	/******************************************************************************/
	if instance.Status.LocalSecretVarsPopulated != nil {
		instance.Status.LocalSecretVarsPopulated = nil
	}

	if instance.Status.RedHatMarketplaceSecretFound != nil {
		instance.Status.RedHatMarketplaceSecretFound = nil
	}

	if instance.Spec.DeployConfig == nil {
		instance.Spec.DeployConfig = &marketplacev1alpha1.RazeeConfigurationValues{}
	}

	secretName := utils.RHM_OPERATOR_SECRET_NAME

	if instance.Spec.DeploySecretName != nil {
		secretName = *instance.Spec.DeploySecretName
	}

	rhmOperatorSecret := &corev1.Secret{}
	err = r.client.Get(context.TODO(), types.NamespacedName{
		Name:      secretName,
		Namespace: request.Namespace,
	}, rhmOperatorSecret)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Info("Failed to find operator secret")
			return reconcile.Result{RequeueAfter: time.Second * 60}, nil
		} else {
			return reconcile.Result{}, err
		}
	}

	if err := controllerutil.SetControllerReference(instance, rhmOperatorSecret, r.scheme); err != nil {
		reqLogger.Error(err, "error setting controller ref")
		return reconcile.Result{}, err
	}

	razeeConfigurationValues := marketplacev1alpha1.RazeeConfigurationValues{}
	razeeConfigurationValues, missingItems, err := utils.AddSecretFieldsToStruct(rhmOperatorSecret.Data, *instance)
	instance.Status.MissingDeploySecretValues = missingItems
	instance.Spec.DeployConfig = &razeeConfigurationValues

	reqLogger.Info("Updating razee instance with missing items and secret values")
	err = r.client.Update(context.TODO(), instance)
	if err != nil {
		reqLogger.Error(err, "Failed to update Spec.DeploySecretValues")
		return reconcile.Result{}, err
	}

	if len(instance.Status.MissingDeploySecretValues) > 0 {
		reqLogger.Info("Missing required razee configuration values, will wait until the secret is updated")
		return reconcile.Result{}, nil
	}

	reqLogger.Info("all secret values found")

	//construct the childURL
	url := fmt.Sprintf("%s/%s/%s/%s", instance.Spec.DeployConfig.IbmCosURL, instance.Spec.DeployConfig.BucketName, instance.Spec.ClusterUUID, instance.Spec.DeployConfig.ChildRSS3FIleName)
	instance.Spec.ChildUrl = &url
	err = r.client.Update(context.TODO(), instance)
	if err != nil {
		reqLogger.Error(err, "Failed to update ChildUrl")
		return reconcile.Result{}, err
	}

	// Update the Spec TargetNamespace
	reqLogger.Info("All required razee configuration values have been found")

	/******************************************************************************
	APPLY OR UPDATE RAZEE RESOURCES
	/******************************************************************************/
	_, err = r.createOrUpdate(request,instance, "razee" )
	if err != nil {
		reqLogger.Error(err, "createOrUpdateFailed")
	} 
	_,err = r.createOrUpdate(request,instance, utils.WATCH_KEEPER_NON_NAMESPACED_NAME )
	if err != nil {
		reqLogger.Error(err, "createOrUpdateFailed")
	} 

	_,err = r.createOrUpdate(request,instance, utils.WATCH_KEEPER_LIMITPOLL_NAME )
	if err != nil {
		reqLogger.Error(err, "createOrUpdateFailed")
	} 


	_,err = r.createOrUpdate(request,instance, utils.RAZEE_CLUSTER_METADATA_NAME )
	if err != nil {
		reqLogger.Error(err, "createOrUpdateFailed")
	} 

	_,err = r.createOrUpdate(request,instance, utils.WATCH_KEEPER_CONFIG_NAME )
	if err != nil {
		reqLogger.Error(err, "createOrUpdateFailed")
	} 


	_,err = r.createOrUpdate(request,instance, utils.WATCH_KEEPER_SECRET_NAME )
	if err != nil {
		reqLogger.Error(err, "createOrUpdateFailed")
	} 

	_,err = r.createOrUpdate(request,instance, utils.COS_READER_KEY_NAME )
	if err != nil {
		reqLogger.Error(err, "createOrUpdateFailed")
	} 

	
	/******************************************************************************
	CREATE THE RAZEE JOB
	/******************************************************************************/
	reqLogger.Info("Finding RazeeDeploy Job")
	job := r.makeRazeeJob(request, instance)

	// Check if the Job exists already
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      utils.RAZEE_DEPLOY_JOB_NAME,
			Namespace: request.Namespace,
		},
	}

	foundJob := batch.Job{}
	err = r.client.Get(context.TODO(), req.NamespacedName, &foundJob)

	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Resource does not exist", "resource: ", utils.RAZEE_JOB_NAME)
		err = r.client.Create(context.TODO(), job)
		if err != nil {
			reqLogger.Error(err, "Failed to create Job on cluster")
			return reconcile.Result{}, err
		}
		reqLogger.Info("job created successfully")
		// requeue to grab the "foundJob" and continue to update status
		// wait 30 seconds so the job has time to complete
		// not entirely necessary, but the struct on Status.Conditions needs the Conditions in the job to be populated.
		return reconcile.Result{RequeueAfter: time.Second * 30}, nil
		// return reconcile.Result{Requeue: true}, nil
		// return reconcile.Result{}, nil
	} else if err != nil {
		reqLogger.Error(err, "Failed to get Job(s) from Cluster")
		return reconcile.Result{}, err
	}

	if len(foundJob.Status.Conditions) == 0 {
		reqLogger.Info("RazeeJob Conditions have not been propagated yet")
		return reconcile.Result{RequeueAfter: time.Second * 30}, nil
	}

	if err := controllerutil.SetControllerReference(instance, &foundJob, r.scheme); err != nil {
		reqLogger.Error(err, "Failed to set controller reference")
		return reconcile.Result{}, err
	}

	// if the conditions have populated then update status
	if len(foundJob.Status.Conditions) != 0 {
		reqLogger.Info("RazeeJob Conditions have been propagated")
		// Update status and conditions
		instance.Status.JobState = foundJob.Status
		for _, jobCondition := range foundJob.Status.Conditions {
			instance.Status.Conditions = &jobCondition
		}

		secretName := "rhm-operator-secret"

		if instance.Spec.DeploySecretName != nil {
			secretName = *instance.Spec.DeploySecretName
		}

		instance.Status.RazeeJobInstall = &marketplacev1alpha1.RazeeJobInstallStruct{
			RazeeNamespace:  secretName,
			RazeeInstallURL: instance.Spec.DeployConfig.FileSourceURL,
		}

		err = r.client.Status().Update(context.TODO(), instance)
		if err != nil {
			reqLogger.Error(err, "Failed to update JobState")
			return reconcile.Result{}, err
		}

		reqLogger.Info("Updated JobState")
		
	}


	// if the job succeeds apply the parentRRS3 and patch the Infrastructure and Console resources
	// if instance.Status.JobState.Succeeded == 1 {
	if foundJob.Status.Succeeded == 1 {
		result, err := r.createOrUpdate(request,instance, utils.PARENT_RRS3_RESOURCE_NAME)
		if err != nil {
			reqLogger.Error(err, "createOrUpdateFailed")
		} 
		if result.Requeue {
			return result, nil
		}
	
		/******************************************************************************
		PATCH RESOURCES FOR DIANEMO
		Patch the Console and Infrastructure resources with the watch-keeper label
		Patch 'razee-cluster-metadata' with ClusterUUID
		/******************************************************************************/
		reqLogger.Info("finding Console resource")
		console := &unstructured.Unstructured{}
		console.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "config.openshift.io",
			Kind:    "Console",
			Version: "v1",
		})
		err = r.client.Get(context.Background(), client.ObjectKey{
			Name: "cluster",
		}, console)
		if err != nil {
			reqLogger.Error(err, "Failed to retrieve Console resource")
			return reconcile.Result{}, err
		}

		reqLogger.Info("Found Console resource")
		consoleLabels := console.GetLabels()

		if !reflect.DeepEqual(consoleLabels, RAZEE_WATCH_KEEPER_LABELS) || consoleLabels == nil {
			console.SetLabels(RAZEE_WATCH_KEEPER_LABELS)
			err = r.client.Update(context.TODO(), console)
			if err != nil {
				reqLogger.Error(err, "Failed to patch Console resource")
				return reconcile.Result{}, err
			}
			reqLogger.Info("Patched Console resource")
		}
		reqLogger.Info("No patch needed on Console resource")

		reqLogger.Info("finding Infrastructure resource")
		infrastructureResource := &unstructured.Unstructured{}
		infrastructureResource.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "config.openshift.io",
			Kind:    "Infrastructure",
			Version: "v1",
		})
		err = r.client.Get(context.Background(), client.ObjectKey{
			Name: "cluster",
		}, infrastructureResource)
		if err != nil {
			reqLogger.Error(err, "Failed to retrieve Infrastructure resource")
			return reconcile.Result{}, err
		}

		reqLogger.Info("Found Infrastructure resource")
		infrastructureLabels := infrastructureResource.GetLabels()
		if !reflect.DeepEqual(infrastructureLabels, RAZEE_WATCH_KEEPER_LABELS) || infrastructureLabels == nil {
			infrastructureResource.SetLabels(RAZEE_WATCH_KEEPER_LABELS)
			err = r.client.Update(context.TODO(), infrastructureResource)
			if err != nil {
				reqLogger.Error(err, "Failed to patch Infrastructure resource")
				return reconcile.Result{}, err
			}
			reqLogger.Info("Patched Infrastructure resource")
		}
		reqLogger.Info("No patch needed on Infrastructure resource")
		fmt.Println("JOB HAS SUCCEEDED TEST")
	}
	
	reqLogger.Info("End of reconcile")
	return reconcile.Result{}, nil

}

// finalizeRazeeDeployment cleans up resources before the RazeeDeployment CR is deleted
func (r *ReconcileRazeeDeployment) finalizeRazeeDeployment(req *marketplacev1alpha1.RazeeDeployment) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name)
	reqLogger.Info("running finalizer")

	jobName := types.NamespacedName{
		Name:      "razeedeploy-job",
		Namespace: req.Namespace,
	}

	foundJob := batch.Job{}
	reqLogger.Info("finding install job")
	err := r.client.Get(context.TODO(), jobName, &foundJob)
	if err != nil {
		if !errors.IsNotFound(err) {
			return reconcile.Result{}, err
		}
	}

	if !errors.IsNotFound(err) {
		reqLogger.Info("cleaning up install job")
		err := r.client.Delete(context.TODO(), &foundJob, client.PropagationPolicy(metav1.DeletePropagationBackground))
		if err != nil && !errors.IsNotFound(err) {
			reqLogger.Error(err, "cleaning up install job")
			return reconcile.Result{}, err
		}
	} else {
		reqLogger.Info("found no job to clean up")
	}

	// Deploy a job to delete razee if we need to
	if req.Status.RazeeJobInstall != nil {
		jobName.Name = utils.RAZEE_UNINSTALL_NAME
		foundJob = batch.Job{}
		reqLogger.Info("razee was installed; finding uninstall job")
		err = r.client.Get(context.TODO(), jobName, &foundJob)
		if err != nil && errors.IsNotFound(err) {
			reqLogger.Info("Creating razee-uninstall-job")
			job := r.makeRazeeUninstallJob(req.Namespace, req.Status.RazeeJobInstall)
			err = r.client.Create(context.TODO(), job)
			if err != nil {
				reqLogger.Error(err, "Failed to create Job on cluster")
				return reconcile.Result{}, err
			}
			reqLogger.Info("job created successfully")
			return reconcile.Result{RequeueAfter: time.Second * 5}, nil
		} else if err != nil {
			reqLogger.Error(err, "Failed to get Job(s) from Cluster")
			return reconcile.Result{}, err
		}

		reqLogger.Info("found uninstall job")

		if len(foundJob.Status.Conditions) == 0 {
			reqLogger.Info("RazeeUninstallJob Conditions have not been propagated yet")
			return reconcile.Result{RequeueAfter: time.Second * 30}, nil
		}

		if foundJob.Status.Succeeded < 1 && foundJob.Status.Failed <= 3 {
			reqLogger.Info("RazeeUnisntallJob is not successful")
			return reconcile.Result{RequeueAfter: time.Second * 30}, nil
		}

		reqLogger.Info("Deleteing uninstall job")
		err = r.client.Delete(context.TODO(), &foundJob, client.PropagationPolicy(metav1.DeletePropagationBackground))
		if err != nil {
			if !errors.IsNotFound(err) {
				return reconcile.Result{}, err
			}
		}
	}

	reqLogger.Info("Uninstall job created successfully")
	reqLogger.Info("Successfully finalized RazeeDeployment")

	// Remove the RAZEE_DEPLOYMENT_FINALIZER
	// Once all finalizers are removed, the object will be deleted
	req.SetFinalizers(utils.RemoveKey(req.GetFinalizers(), utils.RAZEE_DEPLOYMENT_FINALIZER))
	err = r.client.Update(context.TODO(), req)
	if err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

type CreateResourceFunctions struct { 
    makeWatchKeeperNonNamespace func(*marketplacev1alpha1.RazeeDeployment)*corev1.ConfigMap
    Pending   func(int, int) int
} 

func (r *ReconcileRazeeDeployment) createOrUpdate(request reconcile.Request,instance *marketplacev1alpha1.RazeeDeployment,resourceName string)(reconcile.Result,error){ 
        switch resourceName {
			case "razee":
				razeeNamespace := &corev1.Namespace{}
				ns := types.NamespacedName{Name: *instance.Spec.TargetNamespace}
				exists,err := r.checkIfResourceExists(request, razeeNamespace, ns.String())
				if err != nil {
					return reconcile.Result{},err
				}
				if !exists {
					razeeNamespace.ObjectMeta.Name = *instance.Spec.TargetNamespace
					err := r.applyResourceIfNotFound(request, razeeNamespace)
					if err != nil{
						return reconcile.Result{},err
					}

					err = r.addToStatusIfNotPresent(instance, request, "razee")
					if err != nil {
						return reconcile.Result{},err
					}
				} 

			case utils.WATCH_KEEPER_NON_NAMESPACED_NAME:
				watchKeeperNonNamespace := corev1.ConfigMap{}
				exists,err := r.checkIfResourceExists(request, &watchKeeperNonNamespace, utils.WATCH_KEEPER_NON_NAMESPACED_NAME)
				if err != nil {
					return reconcile.Result{},err
				}
				if !exists {
					watchKeeperNonNamespace = *r.makeWatchKeeperNonNamespace(instance)
					err := r.applyResourceIfNotFound(request, &watchKeeperNonNamespace)
					if err != nil{
						return reconcile.Result{},err
					}
				} 
				
				updatedWatchKeeperNonNameSpace := r.makeWatchKeeperNonNamespace(instance)
				err = r.updateOnChange(*instance, request, &watchKeeperNonNamespace,updatedWatchKeeperNonNameSpace, utils.WATCH_KEEPER_NON_NAMESPACED_NAME)
				if err != nil{
					return reconcile.Result{},err
				}
		
			case utils.WATCH_KEEPER_LIMITPOLL_NAME:
				watchKeeperLimitPoll := corev1.ConfigMap{}
				exists, err := r.checkIfResourceExists(request, &watchKeeperLimitPoll, utils.WATCH_KEEPER_LIMITPOLL_NAME)
				if err != nil {
					return reconcile.Result{},err
				}
				if !exists{
					watchKeeperLimitPoll = *r.makeWatchKeeperLimitPoll(instance)
					err = r.applyResourceIfNotFound(request, &watchKeeperLimitPoll)
					if err != nil {
						return reconcile.Result{},err
					}

				} 
				
				updatedWatchKeeperLimitPoll := r.makeWatchKeeperLimitPoll(instance)
				err = r.updateOnChange(*instance, request, &watchKeeperLimitPoll,updatedWatchKeeperLimitPoll,utils.WATCH_KEEPER_LIMITPOLL_NAME)
				if err != nil{
					return reconcile.Result{},err
				}
			case utils.RAZEE_CLUSTER_METADATA_NAME: 
				razeeClusterMetadata := corev1.ConfigMap{}
				exists, err := r.checkIfResourceExists(request, &razeeClusterMetadata, utils.RAZEE_CLUSTER_METADATA_NAME)
				if err != nil {
					return reconcile.Result{},err 
				}
				if !exists{
					razeeClusterMetadata = *r.makeRazeeClusterMetaData(instance)
					err = r.applyResourceIfNotFound(request, &razeeClusterMetadata)
					if err != nil {
						return reconcile.Result{},err
					}

				} 
				
				updatedRazeeClusterMetadata := r.makeRazeeClusterMetaData(instance)
				err = r.updateOnChange(*instance, request, &razeeClusterMetadata,updatedRazeeClusterMetadata,utils.RAZEE_CLUSTER_METADATA_NAME)
				if err != nil{
					return reconcile.Result{},err
				}
			case utils.WATCH_KEEPER_CONFIG_NAME:
				watchKeeperConfig := corev1.ConfigMap{}
				exists, err := r.checkIfResourceExists(request, &watchKeeperConfig, utils.WATCH_KEEPER_CONFIG_NAME)
				if err != nil {
					return reconcile.Result{},err
				}
				if !exists{
					watchKeeperConfig = *r.makeWatchKeeperConfig(instance)
					err = r.applyResourceIfNotFound(request, &watchKeeperConfig)
					if err != nil {
						return reconcile.Result{},err
					}

				} 
				
				updatedWatchKeeperConfig := r.makeWatchKeeperConfig(instance)
				if err != nil {
					return reconcile.Result{},err
				}
				err = r.updateOnChange(*instance, request, &watchKeeperConfig,updatedWatchKeeperConfig,utils.WATCH_KEEPER_CONFIG_NAME)
				if err != nil{
					return reconcile.Result{},err
				}
			case utils.WATCH_KEEPER_SECRET_NAME: 
				watchKeeperSecret := corev1.Secret{}
				exists, err := r.checkIfResourceExists(request, &watchKeeperSecret, utils.WATCH_KEEPER_SECRET_NAME)
				if err != nil {
					return reconcile.Result{},err 
				}
				if !exists{
					watchKeeperSecret, err = r.makeWatchKeeperSecret(instance,request)
					if err != nil {
						return reconcile.Result{},err
					}
					err = r.applyResourceIfNotFound(request, &watchKeeperSecret)
					if err != nil {
						return reconcile.Result{},err
					}
				} 
				
				updatedWatchKeeperSecret,err := r.makeWatchKeeperSecret(instance, request)
				if err != nil {
					return reconcile.Result{},err
				}
				err = r.updateOnChange(*instance, request, &watchKeeperSecret,&updatedWatchKeeperSecret,utils.WATCH_KEEPER_SECRET_NAME)
				if err != nil{
					return reconcile.Result{},err
				}
			case utils.COS_READER_KEY_NAME:
				ibmCosReaderKey := corev1.Secret{}
				exists, err := r.checkIfResourceExists(request, &ibmCosReaderKey, utils.COS_READER_KEY_NAME)
				if err != nil {
					return reconcile.Result{},err
				}
				if !exists{
					ibmCosReaderKey,err = r.makeCOSReaderSecret(instance,request)
					if err != nil {
						return reconcile.Result{},err
					}
					err = r.applyResourceIfNotFound(request, &ibmCosReaderKey)
					if err != nil {
						return reconcile.Result{},err
					}

				} 
				
				updatedibmCosReaderKey, err := r.makeCOSReaderSecret(instance, request)
				if err != nil {
					return reconcile.Result{},err
				}
				err = r.updateOnChange(*instance, request, &ibmCosReaderKey,&updatedibmCosReaderKey,utils.COS_READER_KEY_NAME)
				if err != nil {
					return reconcile.Result{},err
				}
			case utils.PARENT_RRS3_RESOURCE_NAME:
				parentRRS3 := &unstructured.Unstructured{}
				parentRRS3.SetGroupVersionKind(schema.GroupVersionKind{
					Group:   "deploy.razee.io",
					Kind:    "RemoteResourceS3",
					Version: "v1alpha2",
				})

				exists, err := r.checkIfResourceExists(request, parentRRS3, utils.PARENT_RRS3_RESOURCE_NAME)
				if err != nil {
					return reconcile.Result{},err
				}
				if !exists{
					parentRRS3 = r.makeParentRemoteResourceS3(instance)
					err = r.applyResourceIfNotFound(request, parentRRS3)
					if err != nil {
						return reconcile.Result{},err
					}

				}

				updatedParentRRS3 := r.makeParentRemoteResourceS3(instance)
				updatedParentRRS3.SetAnnotations(parentRRS3.GetAnnotations())
				updatedParentRRS3.SetCreationTimestamp(parentRRS3.GetCreationTimestamp())
				updatedParentRRS3.SetFinalizers(parentRRS3.GetFinalizers())
				updatedParentRRS3.SetGeneration(parentRRS3.GetGeneration())
				updatedParentRRS3.SetResourceVersion(parentRRS3.GetResourceVersion())
				updatedParentRRS3.SetSelfLink(parentRRS3.GetSelfLink())
				updatedParentRRS3.SetUID(parentRRS3.GetUID())

				err = r.updateUnstructuredResourceOnChange(*instance, request, parentRRS3,updatedParentRRS3,utils.PARENT_RRS3_RESOURCE_NAME)
				if err != nil {
					return reconcile.Result{},err
				}
        
	}
	
	return reconcile.Result{Requeue: true},nil
}

func (r *ReconcileRazeeDeployment) applyResourceIfNotFound(request reconcile.Request, resource runtime.Object)error{
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Resource does not exist", "resource: ", utils.WATCH_KEEPER_NON_NAMESPACED_NAME)
	if err := utils.ApplyAnnotation(resource); err != nil {
		return err
	}

	err := r.client.Create(context.TODO(), resource)
	if err != nil {
		return err
	}

	return nil
}

func(r *ReconcileRazeeDeployment) addToStatusIfNotPresent(instance *marketplacev1alpha1.RazeeDeployment, request reconcile.Request,resourceName string)(error){
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	if !utils.Contains(instance.Status.RazeePrerequisitesCreated,resourceName) {
		instance.Status.RazeePrerequisitesCreated = append(instance.Status.RazeePrerequisitesCreated, resourceName)
		reqLogger.Info("updating Spec.RazeePrerequisitesCreated")
		err := r.client.Status().Update(context.TODO(), instance)
		if err != nil {
			reqLogger.Error(err, "Failed to update status")
			return err
		}
	}

	return nil
}


func(r *ReconcileRazeeDeployment) updateOnChange(instance marketplacev1alpha1.RazeeDeployment,request reconcile.Request, currentResource runtime.Object,updatedResource runtime.Object, resourceName string)error{
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	patchResult, err := patch.DefaultPatchMaker.Calculate(currentResource, updatedResource)
		if err != nil {
			reqLogger.Error(err, "Failed to compare patches")
			return err
		}

		if !patchResult.IsEmpty() {
			reqLogger.Info("Change detected on resource", "resource: ", resourceName)
			if err := utils.ApplyAnnotation(updatedResource); err != nil {
				reqLogger.Error(err, "Failed to set annotation")
				return err
			}

			reqLogger.Info("Updating resource", "resource: ", resourceName)
			err = r.client.Update(context.TODO(), updatedResource)
			if err != nil {
				reqLogger.Error(err, "Failed to update resource", "resource: ", resourceName)
				return err
			}
		}

		reqLogger.Info("No change detected on resource", "resource: ", resourceName)

		err = r.addToStatusIfNotPresent(&instance,request,resourceName)
		if err != nil {
			return err
		}
		return nil
}

func(r *ReconcileRazeeDeployment) updateUnstructuredResourceOnChange(instance marketplacev1alpha1.RazeeDeployment,request reconcile.Request, currentResource interface{},updatedResource interface{}, resourceName string)error{
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	patchResult, err := patch.DefaultPatchMaker.Calculate(currentResource.(runtime.Object), updatedResource.(runtime.Object),patch.IgnoreStatusFields())
		if err != nil {
			reqLogger.Error(err, "Failed to compare patches")
			return err
		}

		if !patchResult.IsEmpty() {
			reqLogger.Info("Change detected on resource", "resource: ", resourceName)
			currentResource.(unstructured.Unstructured).Object["spec"] = updatedResource.(unstructured.Unstructured).Object["spec"]
			if err := utils.ApplyAnnotation(updatedResource.(runtime.Object)); err != nil {
				reqLogger.Error(err, "Failed to set annotation")
				return err
			}

			reqLogger.Info("Updating resource", "resource: ", resourceName)
			err = r.client.Update(context.TODO(), updatedResource.(runtime.Object))
			if err != nil {
				reqLogger.Error(err, "Failed to update resource", "resource: ", resourceName)
				return err
			}
		}

		reqLogger.Info("No change detected on resource", "resource: ", resourceName)

		err = r.addToStatusIfNotPresent(&instance, request,resourceName)
		if err != nil {
			return err
		}

		return nil
}

//if resource already exists, check diff on annotations and update if need be
func (r *ReconcileRazeeDeployment)checkIfResourceExists(request reconcile.Request,resource runtime.Object,resourceName string)(bool,error){
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "request.Name", request.Name)

	err := r.client.Get(context.TODO(), types.NamespacedName{Name: resourceName, Namespace: request.Namespace}, resource)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Info("Resource does not exist", "resource: ", resourceName)
			return false, nil
		} else {
			reqLogger.Error(err, "Failed to get ","resource: ",resourceName)
			return false, err
		}
	}
	if err == nil{
		reqLogger.Info("Resource already exists", "resource: ", resourceName)
		return true, nil

	}

	return true, nil
}

// Creates the razeedeploy-job and applies the FileSourceUrl and TargetNamespace off the Razeedeployment cr
func (r *ReconcileRazeeDeployment) makeRazeeJob(
	request reconcile.Request,
	instance *marketplacev1alpha1.RazeeDeployment,
) *batch.Job {
	return &batch.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      utils.RAZEE_DEPLOY_JOB_NAME,
			Namespace: request.Namespace,
		},
		Spec: batch.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					ServiceAccountName: utils.RAZEE_SERVICE_ACCOUNT,
					Containers: []corev1.Container{{
						Name:    utils.RAZEE_DEPLOY_JOB_NAME,
						Image:   r.opts.RazeeJobImage,
						Command: []string{"node", "src/install", fmt.Sprintf("--namespace=%s", *instance.Spec.TargetNamespace)},
						Args:    []string{fmt.Sprintf("--file-source=%v", instance.Spec.DeployConfig.FileSourceURL), "--autoupdate"},
					}},
					RestartPolicy: "Never",
				},
			},
		},
	}
}

// MakeRazeeUninstalllJob returns a Batch.Job which uninstalls razee
func (r *ReconcileRazeeDeployment) makeRazeeUninstallJob(namespace string, razeeJob *marketplacev1alpha1.RazeeJobInstallStruct) *batch.Job {
	return &batch.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      utils.RAZEE_UNINSTALL_NAME,
			Namespace: namespace,
		},
		Spec: batch.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					ServiceAccountName: utils.RAZEE_SERVICE_ACCOUNT,
					Containers: []corev1.Container{{
						Name:    utils.RAZEE_UNINSTALL_NAME,
						Image:   r.opts.RazeeJobImage,
						Command: []string{"node", "src/remove", fmt.Sprintf("--namespace=%s", razeeJob.RazeeNamespace)},
						Args:    []string{fmt.Sprintf("--file-source=%v", razeeJob.RazeeInstallURL), "--autoupdate"},
					}},
					RestartPolicy: "Never",
				},
			},
		},
	}
}

// addFinalizer adds finalizers to the RazeeDeployment CR
func (r *ReconcileRazeeDeployment) addFinalizer(razee *marketplacev1alpha1.RazeeDeployment, namespace string) error {
	reqLogger := log.WithValues("Request.Namespace", namespace, "Request.Name", utils.RAZEE_UNINSTALL_NAME)
	reqLogger.Info("Adding Finalizer for the razeeDeploymentFinzliaer")
	razee.SetFinalizers(append(razee.GetFinalizers(), utils.RAZEE_DEPLOYMENT_FINALIZER))

	err := r.client.Update(context.TODO(), razee)
	if err != nil {
		reqLogger.Error(err, "Failed to update RazeeDeployment with the Finalizer")
		return err
	}
	return nil
}

// Creates the razee-cluster-metadata config map and applies the TargetNamespace and the ClusterUUID stored on the Razeedeployment cr
func (r *ReconcileRazeeDeployment) makeRazeeClusterMetaData(instance *marketplacev1alpha1.RazeeDeployment) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      utils.RAZEE_CLUSTER_METADATA_NAME,
			Namespace: *instance.Spec.TargetNamespace,
			Labels: map[string]string{
				"razee/cluster-metadata": "true",
				"razee/watch-resource":   "lite",
			},
		},
		Data: map[string]string{"name": instance.Spec.ClusterUUID},
	}
}

//watch-keeper-non-namespace
func (r *ReconcileRazeeDeployment) makeWatchKeeperNonNamespace(
	instance *marketplacev1alpha1.RazeeDeployment,
) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      utils.WATCH_KEEPER_NON_NAMESPACED_NAME,
			Namespace: *instance.Spec.TargetNamespace,
		},
		Data: map[string]string{"v1_namespace": "true"},
	}
}

//watch-keeper-non-namespace
func (r *ReconcileRazeeDeployment) makeWatchKeeperLimitPoll(
	instance *marketplacev1alpha1.RazeeDeployment,
) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      utils.WATCH_KEEPER_LIMITPOLL_NAME,
			Namespace: *instance.Spec.TargetNamespace,
		},
	}
}

// Creates watchkeeper config and applies the razee-dash-url stored on the Razeedeployment cr
func (r *ReconcileRazeeDeployment) makeWatchKeeperConfig(instance *marketplacev1alpha1.RazeeDeployment) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      utils.WATCH_KEEPER_CONFIG_NAME,
			Namespace: *instance.Spec.TargetNamespace,
		},
		Data: map[string]string{"RAZEEDASH_URL": instance.Spec.DeployConfig.RazeeDashUrl, "START_DELAY_MAX": "0"},
	}
}

// Uses the SecretKeySelector struct to to retrieve byte data from a specified key
func (r *ReconcileRazeeDeployment) GetDataFromRhmSecret(request reconcile.Request, sel corev1.SecretKeySelector) ([]byte, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "request.Name", request.Name)
	rhmOperatorSecret := corev1.Secret{}
	err := r.client.Get(context.TODO(), types.NamespacedName{
		Name:      utils.RHM_OPERATOR_SECRET_NAME,
		Namespace: request.Namespace,
	}, &rhmOperatorSecret)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Error(err, "Failed to find operator secret")
			return nil, err
		}
		return nil, err
	}
	key, err := utils.ExtractCredKey(&rhmOperatorSecret, sel)
	return key, err
}

// Creates the watch-keeper-secret and applies the razee-dash-org-key stored on the rhm-operator-secret using the selector stored on the Razeedeployment cr
func (r *ReconcileRazeeDeployment) makeWatchKeeperSecret(instance *marketplacev1alpha1.RazeeDeployment, request reconcile.Request) (corev1.Secret, error) {
	selector := instance.Spec.DeployConfig.RazeeDashOrgKey
	key, err := r.GetDataFromRhmSecret(request, *selector)

	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      utils.WATCH_KEEPER_SECRET_NAME,
			Namespace: *instance.Spec.TargetNamespace,
		},
		Data: map[string][]byte{"RAZEEDASH_ORG_KEY": key},
	}, err
}

// Creates the rhm-cos-reader-key and applies the ibm-cos-reader-key from rhm-operator-secret using the selector stored on the Razeedeployment cr
func (r *ReconcileRazeeDeployment) makeCOSReaderSecret(instance *marketplacev1alpha1.RazeeDeployment, request reconcile.Request) (corev1.Secret, error) {
	selector := instance.Spec.DeployConfig.IbmCosReaderKey
	key, err := r.GetDataFromRhmSecret(request, *selector)

	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      utils.COS_READER_KEY_NAME,
			Namespace: *instance.Spec.TargetNamespace,
		},
		Data: map[string][]byte{"accesskey": []byte(key)},
	}, err
}

// Creates the "parent" RemoteResourceS3 and applies the name of the cos-reader-key and ChildUrl constructed during reconciliation of the rhm-operator-secret
func (r *ReconcileRazeeDeployment) makeParentRemoteResourceS3(instance *marketplacev1alpha1.RazeeDeployment) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "deploy.razee.io/v1alpha2",
			"kind":       "RemoteResourceS3",
			"metadata": map[string]interface{}{
				"name":      utils.PARENT_RRS3_RESOURCE_NAME,
				"namespace": *instance.Spec.TargetNamespace,
			},
			"spec": map[string]interface{}{
				"auth": map[string]interface{}{
					"iam": map[string]interface{}{
						"responseType": "cloud_iam",
						"url":          `https://iam.cloud.ibm.com/identity/token`,
						"grantType":    "urn:ibm:params:oauth:grant-type:apikey",
						"apiKeyRef": map[string]interface{}{
							"valueFrom": map[string]interface{}{
								"secretKeyRef": map[string]interface{}{
									"name": utils.COS_READER_KEY_NAME,
									"key":  "accesskey",
								},
							},
						},
					},
				},
				"requests": []interface{}{
					map[string]map[string]string{"options": {"url": *instance.Spec.ChildUrl}},
				},
			},
		},
	}
}

// fullUninstall deletes the watch-keeper ConfigMap and then the watch-keeper Deployment
func (r *ReconcileRazeeDeployment) fullUninstall(
	req *marketplacev1alpha1.RazeeDeployment,
) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name)
	reqLogger.Info("Starting partial uninstall of razee")

	deletePolicy := metav1.DeletePropagationForeground

	reqLogger.Info("Deleting rrs3")
	rrs3 := &unstructured.Unstructured{}
	rrs3.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "deploy.razee.io",
		Kind:    "RemoteResourceS3",
		Version: "v1alpha2",
	})

	reqLogger.Info("Patching rrs3 child")

	err := r.client.Get(context.TODO(), types.NamespacedName{
		Name:      "child",
		Namespace: *req.Spec.TargetNamespace,
	}, rrs3)

	if err == nil {
		reqLogger.Info("found child rrs3, patching reconcile=false")

		childLabels := rrs3.GetLabels()

		reconcileVal, ok := childLabels["deploy.razee.io/Reconcile"]

		if !ok || (ok && reconcileVal != "false") {
			rrs3.SetLabels(map[string]string{
				"deploy.razee.io/Reconcile": "false",
			})

			err = r.client.Update(context.TODO(), rrs3)
			if err != nil {
				reqLogger.Error(err, "error updating child resource")
			} else {
				// requeue so the label can take affect
				return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
			}
		}
	}

	reqLogger.Info("Deleteing rrs3")
	rrs3Names := []string{"parent", "child"}

	for _, rrs3Name := range rrs3Names {
		err = r.client.Get(context.TODO(), types.NamespacedName{
			Name:      rrs3Name,
			Namespace: *req.Spec.TargetNamespace,
		}, rrs3)

		if err == nil {
			err := r.client.Delete(context.TODO(), rrs3, client.PropagationPolicy(deletePolicy))
			if err != nil {
				if !errors.IsNotFound(err) {
					reqLogger.Error(err, "could not delete rrs3 resource", "name", rrs3Name)
				}
			}
		}
	}

	reqLogger.Info("Deleting rr")
	rr := &unstructured.Unstructured{}
	rr.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "deploy.razee.io",
		Kind:    "RemoteResource",
		Version: "v1alpha2",
	})

	err = r.client.Get(context.TODO(), types.NamespacedName{
		Name:      "razeedeploy-auto-update",
		Namespace: *req.Spec.TargetNamespace,
	}, rr)

	if err != nil {
		reqLogger.Error(err, "razeedeploy-auto-update not found with error")
	}

	if err == nil {
		err := r.client.Delete(context.TODO(), rr, client.PropagationPolicy(deletePolicy))
		if err != nil {
			if !errors.IsNotFound(err) {
				reqLogger.Error(err, "could not delete watch-keeper rr resource")
			}
		}
	}

	watchKeeperConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "watch-keeper-config",
			Namespace: *req.Spec.TargetNamespace,
		},
	}
	reqLogger.Info("deleting watch-keeper configMap")
	err = r.client.Delete(context.TODO(), watchKeeperConfig)
	if err != nil {
		if err != nil {
			reqLogger.Error(err, "could not delete watch-keeper configmap")
		}
	}

	serviceAccounts := []string{
		"razeedeploy-sa",
		"watch-keeper-sa",
	}

	for _, saName := range serviceAccounts {
		serviceAccount := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: *req.Spec.TargetNamespace,
			},
		}
		reqLogger.Info("deleting sa", "name", saName)
		err = r.client.Delete(context.TODO(), serviceAccount, client.PropagationPolicy(deletePolicy))
		if err != nil {
			if err != nil {
				reqLogger.Error(err, "could not delete sa", "name", saName)
			}
		}
	}

	deploymentNames := []string{
		"watch-keeper",
		"clustersubscription",
		"featureflagsetld-controller",
		"managedset-controller",
		"mustachetemplate-controller",
		"remoteresource-controller",
		"remoteresources3-controller",
		"remoteresources3decrypt-controller",
	}

	for _, deploymentName := range deploymentNames {
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      deploymentName,
				Namespace: *req.Spec.TargetNamespace,
			},
		}
		reqLogger.Info("deleting deployment", "name", deploymentName)
		err = r.client.Delete(context.TODO(), deployment, client.PropagationPolicy(deletePolicy))
		if err != nil {
			if err != nil {
				reqLogger.Error(err, "could not delete deployment", "name", deploymentName)
			}
		}
	}

	req.SetFinalizers(utils.RemoveKey(req.GetFinalizers(), utils.RAZEE_DEPLOYMENT_FINALIZER))
	err = r.client.Update(context.TODO(), req)
	if err != nil {
		return reconcile.Result{}, err
	}

	reqLogger.Info("Partial uninstall of razee is complete")
	return reconcile.Result{}, nil
}

// partialUninstall() deletes the watch-keeper ConfigMap and then the watch-keeper Deployment
func (r *ReconcileRazeeDeployment) partialUninstall(
	req *marketplacev1alpha1.RazeeDeployment,
) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name)
	reqLogger.Info("Starting partial uninstall of razee")

	reqLogger.Info("Deleting rr")
	rrUpdate := &unstructured.Unstructured{}
	rrUpdate.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "deploy.razee.io",
		Kind:    "RemoteResource",
		Version: "v1alpha2",
	})

	err := r.client.Get(context.Background(), types.NamespacedName{
		Name:      "razeedeploy-auto-update",
		Namespace: *req.Spec.TargetNamespace,
	}, rrUpdate)

	found := true
	if err != nil {
		found = false
		reqLogger.Error(err, "razeedeploy-auto-update not found with error")
	}

	deletePolicy := metav1.DeletePropagationForeground

	if found {
		err := r.client.Delete(context.TODO(), rrUpdate, client.PropagationPolicy(deletePolicy))
		if err != nil {
			if !errors.IsNotFound(err) {
				reqLogger.Error(err, "could not delete watch-keeper rr resource")
			}
		}
	}

	watchKeeperConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "watch-keeper-config",
			Namespace: *req.Spec.TargetNamespace,
		},
	}
	reqLogger.Info("deleting watch-keeper configMap")
	err = r.client.Delete(context.TODO(), watchKeeperConfig)
	if err != nil {
		if !errors.IsNotFound(err) {
			reqLogger.Error(err, "could not delete watch-keeper configmap")
			return reconcile.Result{}, err
		}
	}

	watchKeeperDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "watch-keeper",
			Namespace: *req.Spec.TargetNamespace,
		},
	}
	reqLogger.Info("deleting watch-keeper deployment")
	err = r.client.Delete(context.TODO(), watchKeeperDeployment, client.PropagationPolicy(deletePolicy))
	if err != nil {
		if !errors.IsNotFound(err) {
			reqLogger.Error(err, "could not delete watch-keeper deployment")
			return reconcile.Result{}, err
		}
	}

	req.SetFinalizers(utils.RemoveKey(req.GetFinalizers(), utils.RAZEE_DEPLOYMENT_FINALIZER))
	err = r.client.Update(context.TODO(), req)
	if err != nil {
		return reconcile.Result{}, err
	}

	reqLogger.Info("Partial uninstall of razee is complete")
	return reconcile.Result{}, nil
}
