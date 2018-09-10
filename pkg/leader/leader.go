package leader

import (
	"errors"
	"io/ioutil"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"

	"github.com/sirupsen/logrus"
)

// ErrNoNS indicates that a namespace could not be found for the current
// environment
var ErrNoNS = errors.New("namespace not found for current environment")

// TryBecome behaves like Become, except it will not return an error in the
// case where a namespace cannot be found for the current pod. This is useful
// for a service that might run outside the cluster, for example an operator
// being started with `operator-sdk up local`.
func TryBecome(name string) error {
	err := Become(name)
	if err == ErrNoNS {
		logrus.Warn("leader election disabled; no namespace was detected")
		return nil
	}
	return err
}

// Become ensures that the current pod is the leader within its namespace. It
// continuously tries to create a ConfigMap with the provided name and the
// current pod set as the owner reference. Only one can exist at a time with
// the same name, so the pod that successfully creates the ConfigMap is the
// leader. Upon termination of that pod, the garbage collector will delete the
// ConfigMap, enabling a different pod to become the leader.
func Become(name string) error {
	logrus.Info("trying to become the leader")

	ns, err := myNS()
	if err != nil {
		return err
	}

	client, err := getClientset()
	if err != nil {
		return err
	}

	owner, err := myOwnerRef(client, ns)
	if err != nil {
		return err
	}

	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       ns,
			OwnerReferences: []metav1.OwnerReference{owner},
		},
	}

	// check for existing lock from this pod, in case we got restarted
	existing, err := client.CoreV1().ConfigMaps(ns).Get(name, metav1.GetOptions{})
	switch {
	case err == nil:
		for _, existingOwner := range existing.GetOwnerReferences() {
			if existingOwner.Name == owner.Name {
				logrus.Info("Found existing lock with my name. I was likely restarted.")
				logrus.Info("Continuing as the leader.")
				return nil
			} else {
				logrus.Infof("Found existing lock from %s", existingOwner.Name)
			}
		}
	case apierrors.IsNotFound(err):
		logrus.Info("No pre-existing lock was found.")
	default:
		logrus.Error("unknown error trying to get ConfigMap")
		return err
	}

	// try to create a lock
	for {
		_, err := client.CoreV1().ConfigMaps(ns).Create(cm)
		switch {
		case err == nil:
			logrus.Info("Became the leader.")
			return nil
		case apierrors.IsAlreadyExists(err):
			logrus.Info("Not the leader. Waiting.")
			time.Sleep(time.Second * 1)
		default:
			logrus.Error("unknown error creating configmap")
			return err
		}
	}
}

// getClientset returns a k8sclient.Clientset based on the current in-cluster
// config.
func getClientset() (*k8sclient.Clientset, error) {
	c, err := restclient.InClusterConfig()
	if err != nil {
		return nil, err
	}
	cs, err := k8sclient.NewForConfig(c)
	if err != nil {
		return nil, err
	}
	return cs, nil
}

// myNS returns the name of the namespace in which this code is currently running.
func myNS() (string, error) {
	nsBytes, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNoNS
		}
		return "", err
	}
	ns := strings.TrimSpace(string(nsBytes))
	logrus.Infof("found namespace: %s", ns)
	return ns, nil
}

// myOwnerRef returns an OwnerReference that corresponds to the pod in which
// this code is currently running.
func myOwnerRef(client *k8sclient.Clientset, ns string) (metav1.OwnerReference, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return metav1.OwnerReference{}, err
	}
	logrus.Infof("found hostname: %s", hostname)

	myPod, err := client.CoreV1().Pods(ns).Get(hostname, metav1.GetOptions{})
	if err != nil {
		logrus.Error("failed to get pod")
		return metav1.OwnerReference{}, err
	}

	owner := metav1.OwnerReference{
		APIVersion: "v1",
		Kind:       "Pod",
		Name:       myPod.ObjectMeta.Name,
		UID:        myPod.ObjectMeta.UID,
	}
	return owner, nil
}
