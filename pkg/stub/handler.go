package stub

import (
	"fmt"
	"reflect"
	"context"

	v1alpha1 "github.com/robszumski/prometheus-replica-operator/pkg/apis/prometheus/v1alpha1"

	"github.com/operator-framework/operator-sdk/pkg/sdk"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func NewHandler() sdk.Handler {
	return &Handler{}
}

type Handler struct {
}

func (h *Handler) Handle(ctx context.Context, event sdk.Event) error {
	switch o := event.Object.(type) {
	case *v1alpha1.PrometheusReplica:
		PrometheusReplica := o

		// Ignore the delete event since the garbage collector will clean up all secondary resources for the CR
		// All secondary resources must have the CR set as their OwnerReference for this to be the case
		if event.Deleted {
			return nil
		}

		// SANITY CHECK
		logrus.Infof("Parsing PrometheusReplica %s in %s", PrometheusReplica.Name, PrometheusReplica.Namespace)

        // INSTALL
		// Create the Prometheus StatefulSet if it doesn't exist
		ssProm := statefulSetForPrometheus(PrometheusReplica)
		err := sdk.Create(ssProm)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create Prometheus StatefulSet: %v", err)
		}

		//Create the Prometheus Service if it doesn't exist
		svcProm := serviceForPrometheus(PrometheusReplica)
		err = sdk.Create(svcProm)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create Prometheus Service: %v", err)
		}

		//Create the Thanos peers Service if it doesn't exist
		svcThanosPeers := serviceForThanosPeers(PrometheusReplica)
		err = sdk.Create(svcThanosPeers)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create Thanos peers Service: %v", err)
		}

		//Create the Thanos store StatefulSet if it doesn't exist
		ssThanosStore := statefulSetForThanosStore(PrometheusReplica)
		err = sdk.Create(ssThanosStore)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create Thanos query StatefulSet: %v", err)
		}

		//Create the Thanos store Service if it doesn't exist
		svcThanosStore := serviceForThanosStore(PrometheusReplica)
		err = sdk.Create(svcThanosStore)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create Thanos store Service: %v", err)
		}

		//Create the Thanos query Deployment if it doesn't exist
		depThanosQuery := deploymentForThanosQuery(PrometheusReplica)
		err = sdk.Create(depThanosQuery)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create Thanos query Deployment: %v", err)
		}

		//Create the Thanos query Service if it doesn't exist
		svcThanosQuery := serviceForThanosQuery(PrometheusReplica)
		err = sdk.Create(svcThanosQuery)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create Thanos query Service: %v", err)
		}

		// UPDATE
		// Ensure the deployment size is the same as the spec
		// err = sdk.Get(dep)
		// if err != nil {
		// 	return fmt.Errorf("failed to get deployment: %v", err)
		// }
		// size := PrometheusReplica.Spec.Size
		// if *dep.Spec.Replicas != size {
		// 	dep.Spec.Replicas = &size
		// 	err = sdk.Update(dep)
		// 	if err != nil {
		// 		return fmt.Errorf("failed to update deployment: %v", err)
		// 	}
		// }

		// STATUS
		// Update the PrometheusReplica status with the pod names
		podStatus := make(map[string][]string)
		svcStatus := make(map[string][]string)
		logrus.Infof("Updating PrometheusReplica status for %s", PrometheusReplica.Name)

		// Define Pod label queries
		filterLabelQueries := map[string]string{
			"prometheuses": labels.SelectorFromSet(labelsForPrometheusPods(PrometheusReplica.Name)).String(),
			"queries": labels.SelectorFromSet(labelsForThanosQuery(PrometheusReplica.Name)).String(),
			"stores": labels.SelectorFromSet(labelsForThanosStore(PrometheusReplica.Name)).String(),
		}

		// Execute Pod label queries
		podStatus, err = filterPodList(filterLabelQueries, PrometheusReplica.Namespace)
		if err != nil {
			return fmt.Errorf("failed to list pods for local status: %v", err)
		}

		// Define Service label queries
		filterLabelQueries = map[string]string{
			"grafana": labels.SelectorFromSet(labelsForGrafana(PrometheusReplica.Name)).String(),
			"query": labels.SelectorFromSet(labelsForThanosQuery(PrometheusReplica.Name)).String(),
		}

		svcStatus, err = filterServiceList(filterLabelQueries, PrometheusReplica.Namespace)
		if err != nil {
			return fmt.Errorf("failed to list services for output status: %v", err)
		}

		//TODO: refactor when Ingresses are added
		status := v1alpha1.PrometheusReplicaStatus{
			//TODO: update after reading ready status
			Phase: "creating",
			Local: v1alpha1.PrometheusLocalStatus{
				Prometheuses: podStatus["prometheuses"],
				Stores: podStatus["stores"],
				Queries: podStatus["queries"],
			},
			Output: v1alpha1.PrometheusOutputStatus{
				Grafana: "not implemented yet",				
				Query: fmt.Sprintf("%s.%s.svc.cluster.local", svcStatus["query"][0], PrometheusReplica.Namespace),
			},
		}

		// Update local status if anything has changed
		if !reflect.DeepEqual(status, PrometheusReplica.Status.Local) {

			// Set local status
			PrometheusReplica.Status = status

			// Update CRD
			err := sdk.Update(PrometheusReplica)
			if err != nil {
				return fmt.Errorf("failed to update PrometheusReplica status: %v", err)
			}
		} else {
			logrus.Infof("figure out why this doesn't ever match!")
		}

	}
	return nil
}

// statefulSetForPrometheus returns a PrometheusReplica StatefulSet object
func statefulSetForPrometheus(pr *v1alpha1.PrometheusReplica) *appsv1.StatefulSet {
	//TODO: do we really need a function for this?
	ls := labelsForPrometheusPods(pr.Name)
	retention := pr.Spec.Metrics.Retention
	blockDuration := pr.Spec.Metrics.BlockDuration
	configMapName := pr.Spec.ConfigMap
	secretName := pr.Spec.BucketSecret

	logrus.Infof("Creating Prometheus StatefulSet for %s", pr.Name)

	var replicas int32
	if pr.Spec.HighlyAvailable {
	    replicas = 2
	    logrus.Infof("  StatefulSet: Translating HighlyAvailable to %d replicas", replicas)
	} else {
		replicas = 1
		logrus.Infof("  StatefulSet: No HA. Starting %t replica", replicas)
	}

	logrus.Infof("  StatefulSet: Setting overall metrics retention to %s", retention)
	logrus.Infof("  StatefulSet: Setting duration until upload to storage bucket to %s", blockDuration)
	logrus.Infof("  StatefulSet: Using Prometheus config from ConfigMap %s", configMapName)
	logrus.Infof("  StatefulSet: Using bucket parameters from Secret %s", secretName)

	dep := &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-prometheus", pr.Name),
			Namespace: pr.Namespace,
			Labels:    ls,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: fmt.Sprintf("%s-prometheus", pr.Name),
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
					Annotations: map[string]string{"prometheus.io/scrape": "true","prometheus.io/port": "10902"},
				},
				Spec: v1.PodSpec{
					Affinity: &v1.Affinity{
						PodAntiAffinity: &v1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: []v1.PodAffinityTerm{
								{
									LabelSelector: &metav1.LabelSelector{
										MatchExpressions: []metav1.LabelSelectorRequirement{
											{
												Key:      "app",
												Operator: metav1.LabelSelectorOpIn,
												Values:   []string{"prometheus"},
											},
										},
									},
									TopologyKey: "kubernetes.io/hostname",
								},
							},
						},
					},
					Containers: []v1.Container{{
						Image:   "quay.io/prometheus/prometheus:v2.0.0",
						Name:    "prometheus",
						Args: []string{
							fmt.Sprintf("--storage.tsdb.retention=%s", retention),
					        "--config.file=/etc/prometheus-shared/prometheus.yml",
					        "--storage.tsdb.path=/var/prometheus",
					        fmt.Sprintf("--storage.tsdb.min-block-duration=%s", blockDuration),
					        fmt.Sprintf("--storage.tsdb.max-block-duration=%s", blockDuration),
					        "--web.enable-lifecycle",
						},
						Ports: []v1.ContainerPort{{
							ContainerPort: 9090,
							Name:          "http",
						}},
						VolumeMounts: []v1.VolumeMount{{
							MountPath: "/etc/prometheus-shared", Name: "config-shared",
						}, {
							MountPath: "/var/prometheus", Name: "data",
						}},
					},{
						Image:   "improbable/thanos:master",
						Name:    "thanos-sidecar",
						Args: []string{
							"sidecar",
					        "--log.level=debug",
					        "--tsdb.path=/var/prometheus",
					        "--prometheus.url=http://127.0.0.1:9090",
					        "--cluster.peers=thanos-peers.default.svc.cluster.local:10900",
					        "--reloader.config-file=/etc/prometheus/prometheus.yml.tmpl",
					        "--reloader.config-envsubst-file=/etc/prometheus-shared/prometheus.yml",
					        "--s3.signature-version2",
						},
						Env: []v1.EnvVar{{
							Name: "S3_BUCKET",
							ValueFrom: &v1.EnvVarSource{
								SecretKeyRef: &v1.SecretKeySelector{
									LocalObjectReference: v1.LocalObjectReference{Name: secretName},
									Key:  "s3_bucket",
								},
							},
						},{
							Name: "S3_ENDPOINT",
							ValueFrom: &v1.EnvVarSource{
								SecretKeyRef: &v1.SecretKeySelector{
									LocalObjectReference: v1.LocalObjectReference{Name: secretName},
									Key:  "s3_endpoint",
								},
							},
						},{
							Name: "S3_ACCESS_KEY",
							ValueFrom: &v1.EnvVarSource{
								SecretKeyRef: &v1.SecretKeySelector{
									LocalObjectReference: v1.LocalObjectReference{Name: secretName},
									Key:  "s3_access_key",
								},
							},
						},{
							Name: "S3_SECRET_KEY",
							ValueFrom: &v1.EnvVarSource{
								SecretKeyRef: &v1.SecretKeySelector{
									LocalObjectReference: v1.LocalObjectReference{Name: secretName},
									Key:  "s3_secret_key",
								},
							},
						}},
						Ports: []v1.ContainerPort{{
							ContainerPort: 10902,
							Name:          "http",
						},{
							ContainerPort: 10901,
							Name:          "grpc",
						},{
							ContainerPort: 10900,
							Name:          "cluster",
						}},
						VolumeMounts: []v1.VolumeMount{{
							MountPath: "/etc/prometheus", Name: "config",
						},{
							MountPath: "/etc/prometheus-shared", Name: "config-shared",
						}, {
							MountPath: "/var/prometheus", Name: "data",
						}},
					}},
					Volumes: []v1.Volume{
						{
							Name: "config-shared",
							VolumeSource: v1.VolumeSource{
								EmptyDir: &v1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "data",
							VolumeSource: v1.VolumeSource{
								EmptyDir: &v1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "config",
							VolumeSource: v1.VolumeSource{
								ConfigMap: &v1.ConfigMapVolumeSource{
									LocalObjectReference: v1.LocalObjectReference{Name:configMapName},
								},
							},
						},
					},
				},
			},
		},
	}
	addOwnerRefToObject(dep, asOwner(pr))
	return dep
}

// serviceForPrometheus returns a PrometheusReplica service object
func serviceForPrometheus(pr *v1alpha1.PrometheusReplica) *v1.Service {
	//TODO: do we really need a function for this?
	ls := labelsForPrometheusPods(pr.Name)

	logrus.Infof("Creating Prometheus service for %s", pr.Name)

	svc := &v1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-prometheus", pr.Name),
			Namespace: pr.Namespace,
			Labels:    ls,
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name: "http-prometheus",
				Port: 9090,
				TargetPort: intstr.IntOrString{
					Type:   intstr.Int,
					IntVal: 9090,
				},
			},{
				Name: "http-sidecar-metrics",
				Port: 10902,
				TargetPort: intstr.IntOrString{
					Type:   intstr.String,
					StrVal: "http",
				},
			}},
			Selector: map[string]string{"app": "prometheus"},
			SessionAffinity: "None",
			Type: "ClusterIP",
		},
	}
	addOwnerRefToObject(svc, asOwner(pr))
	return svc
}

// serviceForThanosPeers returns a PrometheusReplica service object
func serviceForThanosPeers(pr *v1alpha1.PrometheusReplica) *v1.Service {
	//TODO: do we really need a function for this?
	ls := labelsForThanosPeers(pr.Name)

	logrus.Infof("Creating Thanos peers service for %s", pr.Name)

	svc := &v1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-thanos-peers", pr.Name),
			Namespace: pr.Namespace,
			Labels:    ls,
		},
		Spec: v1.ServiceSpec{
			ClusterIP: "None",
			Ports: []v1.ServicePort{{
				Name: "cluster",
				Port: 10900,
				TargetPort: intstr.IntOrString{
					Type:   intstr.String,
					StrVal: "cluster",
				},
			}},
			Selector: ls,
			SessionAffinity: "None",
			Type: "ClusterIP",
		},
	}
	addOwnerRefToObject(svc, asOwner(pr))
	return svc
}

// statefulSetForThanosStore returns a PrometheusReplica StatefulSet object
func statefulSetForThanosStore(pr *v1alpha1.PrometheusReplica) *appsv1.StatefulSet {
	ls := labelsForThanosStore(pr.Name)
	secretName := pr.Spec.BucketSecret
	var replicas int32
	replicas = 1

	logrus.Infof("Creating Thanos store StatefulSet for %s", pr.Name)
	logrus.Infof("  StatefulSet: Using bucket parameters from Secret %s", secretName)

	ss := &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-thanos-store", pr.Name),
			Namespace: pr.Namespace,
			Labels:    ls,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: fmt.Sprintf("%s-thanos-store", pr.Name),
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
					Annotations: map[string]string{"prometheus.io/scrape": "true","prometheus.io/port": "10902"},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{{
						Image:   "improbable/thanos:master",
						Name:    "thanos-store",
						Args: []string{
							"store",
							"--log.level=debug",
							"--tsdb.path=/var/thanos/store",
							fmt.Sprintf("--cluster.peers=%s-thanos-peers.%s.svc.cluster.local:10900", pr.Name, pr.Namespace),
						},
						Env: []v1.EnvVar{{
							Name: "S3_BUCKET",
							ValueFrom: &v1.EnvVarSource{
								SecretKeyRef: &v1.SecretKeySelector{
									LocalObjectReference: v1.LocalObjectReference{Name: secretName},
									Key:  "s3_bucket",
								},
							},
						},{
							Name: "S3_ENDPOINT",
							ValueFrom: &v1.EnvVarSource{
								SecretKeyRef: &v1.SecretKeySelector{
									LocalObjectReference: v1.LocalObjectReference{Name: secretName},
									Key:  "s3_endpoint",
								},
							},
						},{
							Name: "S3_ACCESS_KEY",
							ValueFrom: &v1.EnvVarSource{
								SecretKeyRef: &v1.SecretKeySelector{
									LocalObjectReference: v1.LocalObjectReference{Name: secretName},
									Key:  "s3_access_key",
								},
							},
						},{
							Name: "S3_SECRET_KEY",
							ValueFrom: &v1.EnvVarSource{
								SecretKeyRef: &v1.SecretKeySelector{
									LocalObjectReference: v1.LocalObjectReference{Name: secretName},
									Key:  "s3_secret_key",
								},
							},
						}},
						Ports: []v1.ContainerPort{{
							ContainerPort: 10902,
							Name:          "http",
						},{
							ContainerPort: 10901,
							Name:          "grpc",
						},{
							ContainerPort: 10900,
							Name:          "cluster",
						}},
						VolumeMounts: []v1.VolumeMount{{
							MountPath: "/var/thanos/store", Name: "data",
						}},
					}},
					Volumes: []v1.Volume{{
						Name: "data",
						VolumeSource: v1.VolumeSource{
							EmptyDir: &v1.EmptyDirVolumeSource{},
						},
					}},
				},
			},
		},
	}
	addOwnerRefToObject(ss, asOwner(pr))
	return ss
}

// serviceForThanosStore returns a PrometheusReplica service object
func serviceForThanosStore(pr *v1alpha1.PrometheusReplica) *v1.Service {
	ls := labelsForThanosStore(pr.Name)

	logrus.Infof("Creating Thanos store service for %s", pr.Name)

	svc := &v1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-thanos-store", pr.Name),
			Namespace: pr.Namespace,
			//TODO: audit labels
			Labels:    ls,
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name: "http-store",
				Port: 9090,
				TargetPort: intstr.IntOrString{	
					Type:   intstr.String,
					StrVal: "http",
				},
			}},
			Selector: ls,
			SessionAffinity: "None",
			Type: "ClusterIP",
		},
	}
	addOwnerRefToObject(svc, asOwner(pr))
	return svc
}

// deploymentForThanosQuery returns a PrometheusReplica Deployment object
func deploymentForThanosQuery(pr *v1alpha1.PrometheusReplica) *appsv1.Deployment {
	ls := labelsForThanosQuery(pr.Name)
	secretName := pr.Spec.BucketSecret

	logrus.Infof("Creating Thanos query Deployment for %s", pr.Name)
	logrus.Infof("  Deployment: Using bucket parameters from Secret %s", secretName)

	var replicas int32
	if pr.Spec.HighlyAvailable {
	    replicas = 2
	    logrus.Infof("  Deployment: Translating HighlyAvailable to %d replicas", replicas)
	} else {
		replicas = 1
		logrus.Infof("  Deployment: No HA. Starting %t replica", replicas)
	}

	dep := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-thanos-query", pr.Name),
			Namespace: pr.Namespace,
			Labels:    ls,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
					Annotations: map[string]string{"prometheus.io/scrape": "true","prometheus.io/port": "10902"},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{{
						Image:   "improbable/thanos:master",
						Name:    "thanos-query",
						Args: []string{
							"query",
							"--log.level=debug",
							fmt.Sprintf("--cluster.peers=%s-thanos-peers.%s.svc.cluster.local:10900", pr.Name, pr.Namespace),
							"--query.replica-label=replica",
						},
						Ports: []v1.ContainerPort{{
							ContainerPort: 10902,
							Name:          "http",
						},{
							ContainerPort: 10901,
							Name:          "grpc",
						},{
							ContainerPort: 10900,
							Name:          "cluster",
						}},
					}},
				},
			},
		},
	}
	addOwnerRefToObject(dep, asOwner(pr))
	return dep
}

// serviceForThanosStore returns a PrometheusReplica service object
func serviceForThanosQuery(pr *v1alpha1.PrometheusReplica) *v1.Service {
	ls := labelsForThanosQuery(pr.Name)

	logrus.Infof("Creating Thanos query service for %s", pr.Name)

	svc := &v1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-thanos-query", pr.Name),
			Namespace: pr.Namespace,
			//TODO: audit labels
			Labels:    ls,
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name: "http-query",
				Port: 9090,
				TargetPort: intstr.IntOrString{	
					Type:   intstr.String,
					StrVal: "http",
				},
			}},
			Selector: ls,
			SessionAffinity: "None",
			Type: "ClusterIP",
		},
	}
	addOwnerRefToObject(svc, asOwner(pr))
	return svc
}

// labelsForPrometheusReplica returns the labels for selecting the resources
// belonging to the given PrometheusReplica CR name.
func labelsForPrometheusReplica(name string) map[string]string {
	return map[string]string{"PrometheusReplica_cr": name}
}

// labelsForPrometheusPods returns the labels for selecting the resources
// belonging to the given PrometheusReplica Prometheus pods.
func labelsForPrometheusPods(name string) map[string]string {
	return map[string]string{"app": "prometheus", "PrometheusReplica_cr": name, "thanos-peer": "true"}
}

// labelsForThanosPeers returns the labels for selecting the resources
// belonging to the given PrometheusReplica CR's Thanos peers
func labelsForThanosPeers(name string) map[string]string {
	return map[string]string{"PrometheusReplica_cr": name, "thanos-peer": "true"}
}

// labelsForThanosStores returns the labels for selecting the resources
// belonging to the given PrometheusReplica CR's Thanos stores
func labelsForThanosStore(name string) map[string]string {
	return map[string]string{"app": "thanos-store", "PrometheusReplica_cr": name, "thanos-peer": "true"}
}

// labelsForThanosQuery returns the labels for selecting the resources
// belonging to the given PrometheusReplica CR's Thanos queries
func labelsForThanosQuery(name string) map[string]string {
	return map[string]string{"app": "thanos-query", "PrometheusReplica_cr": name, "thanos-peer": "true"}
}

// labelsForGrafana returns the labels for selecting the resources
// belonging to the given PrometheusReplica CR's Thanos queries
func labelsForGrafana(name string) map[string]string {
	//TODO: add CR when we deploy grafana     "PrometheusReplica_cr": name
	return map[string]string{"app": "grafana"}
}

// addOwnerRefToObject appends the desired OwnerReference to the object
func addOwnerRefToObject(obj metav1.Object, ownerRef metav1.OwnerReference) {
	obj.SetOwnerReferences(append(obj.GetOwnerReferences(), ownerRef))
}

// filterPodList returns an array of Pod names that match a label query
func filterPodList(labelQueries map[string]string, ns string) (map[string][]string, error) {
	podNames := map[string][]string{}

	for group, query := range labelQueries {
		//TODO: set more debug levels
		logrus.Infof("group: %s %s", group, query)

		podList := podList()
		listOps := &metav1.ListOptions{LabelSelector: query}
		err := sdk.List(ns, podList, sdk.WithListOptions(listOps))
		if err != nil {
			return nil, fmt.Errorf("Failed to list pods to filter: %v", err)
		}
		podNames[group] = getPodNames(podList.Items)
		//TODO: set more debug levels
		logrus.Infof("names: %s", podNames[group])
	}

	return podNames, nil
}

// filterServiceList returns an array of Service names that match a label query
func filterServiceList(labelQueries map[string]string, ns string) (map[string][]string, error) {
	serviceNames := map[string][]string{}

	for group, query := range labelQueries {
		//TODO: set more debug levels
		//logrus.Infof("group: %s %s", group, query)

		serviceList := serviceList()
		listOps := &metav1.ListOptions{LabelSelector: query}
		err := sdk.List(ns, serviceList, sdk.WithListOptions(listOps))
		if err != nil {
			return nil, fmt.Errorf("Failed to list services to filter: %v", err)
		}
		serviceNames[group] = getServiceNames(serviceList.Items)
		//TODO: set more debug levels
		//logrus.Infof("names: %s", serviceNames[group])
	}

	return serviceNames, nil
}

// asOwner returns an OwnerReference set as the PrometheusReplica CR
func asOwner(m *v1alpha1.PrometheusReplica) metav1.OwnerReference {
	trueVar := true
	return metav1.OwnerReference{
		APIVersion: m.APIVersion,
		Kind:       m.Kind,
		Name:       m.Name,
		UID:        m.UID,
		Controller: &trueVar,
	}
}

// podList returns a v1.PodList object
func podList() *v1.PodList {
	return &v1.PodList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
	}
}

// getPodNames returns the pod names of the array of pods passed in
func getPodNames(pods []v1.Pod) []string {
	var podNames []string
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}

// serviceList returns a v1.PodList object
func serviceList() *v1.ServiceList {
	return &v1.ServiceList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
	}
}

// getServiceNames returns the service names of the array of services passed in
func getServiceNames(services []v1.Service) []string {
	var serviceNames []string
	for _, service := range services {
		serviceNames = append(serviceNames, service.Name)
	}
	return serviceNames
}