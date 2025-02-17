package pod

import (
	"fmt"
	"github.com/arttor/helmify/pkg/cluster"
	"github.com/arttor/helmify/pkg/helmify"
	securityContext "github.com/arttor/helmify/pkg/processor/security-context"
	"github.com/iancoleman/strcase"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"strings"
)

const imagePullPolicyTemplate = "{{ .Values.%[1]s.%[2]s.imagePullPolicy }}"
const envValue = "{{ quote .Values.%[1]s.%[2]s.%[3]s.%[4]s }}"

func ProcessSpec(objName string, appMeta helmify.AppMetadata, spec corev1.PodSpec) (map[string]interface{}, helmify.Values, error) {
	values, err := processPodSpec(objName, appMeta, &spec)
	if err != nil {
		return nil, nil, err
	}

	// replace PVC to templated name
	for i := 0; i < len(spec.Volumes); i++ {
		vol := spec.Volumes[i]
		if vol.PersistentVolumeClaim == nil {
			continue
		}
		tempPVCName := appMeta.TemplatedName(vol.PersistentVolumeClaim.ClaimName)

		spec.Volumes[i].PersistentVolumeClaim.ClaimName = tempPVCName
	}

	// replace container resources with template to values.
	specMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&spec)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: unable to convert podSpec to map", err)
	}
	containers, _, err := unstructured.NestedSlice(specMap, "containers")
	if err != nil {
		return nil, nil, err
	}
	for i := range containers {
		containerName := strcase.ToLowerCamel((containers[i].(map[string]interface{})["name"]).(string))
		res, exists, err := unstructured.NestedMap(values, objName, containerName, "resources")
		if err != nil {
			return nil, nil, err
		}
		if !exists || len(res) == 0 {
			continue
		}
		err = unstructured.SetNestedField(containers[i].(map[string]interface{}), fmt.Sprintf(`{{- toYaml .Values.%s.%s.resources | nindent 10 }}`, objName, containerName), "resources")
		if err != nil {
			return nil, nil, err
		}
	}
	err = unstructured.SetNestedSlice(specMap, containers, "containers")
	if err != nil {
		return nil, nil, err
	}

	if appMeta.Config().ImagePullSecrets {
		if _, defined := specMap["imagePullSecrets"]; !defined {
			specMap["imagePullSecrets"] = "{{ .Values.imagePullSecrets | default list | toJson }}"
			values["imagePullSecrets"] = []string{}
		}
	}

	err = securityContext.ProcessContainerSecurityContext(objName, specMap, &values)
	if err != nil {
		return nil, nil, err
	}

	// process nodeSelector if presented:
	if len(spec.NodeSelector) != 0 {
		err = unstructured.SetNestedField(specMap, fmt.Sprintf(`{{- toYaml .Values.%s.nodeSelector | nindent 8 }}`, objName), "nodeSelector")
		if err != nil {
			return nil, nil, err
		}
		err = unstructured.SetNestedStringMap(values, spec.NodeSelector, objName, "nodeSelector")
		if err != nil {
			return nil, nil, err
		}
	}
	return specMap, values, nil
}

func processPodSpec(name string, appMeta helmify.AppMetadata, pod *corev1.PodSpec) (helmify.Values, error) {
	values := helmify.Values{}
	for i, c := range pod.Containers {
		processed, err := processPodContainer(name, appMeta, c, &values)
		if err != nil {
			return nil, err
		}
		pod.Containers[i] = processed
	}
	for _, v := range pod.Volumes {
		if v.ConfigMap != nil {
			v.ConfigMap.Name = appMeta.TemplatedName(v.ConfigMap.Name)
		}
		if v.Secret != nil {
			v.Secret.SecretName = appMeta.TemplatedName(v.Secret.SecretName)
		}
	}
	pod.ServiceAccountName = appMeta.TemplatedName(pod.ServiceAccountName)

	for i, s := range pod.ImagePullSecrets {
		pod.ImagePullSecrets[i].Name = appMeta.TemplatedName(s.Name)
	}

	return values, nil
}

func processPodContainer(name string, appMeta helmify.AppMetadata, c corev1.Container, values *helmify.Values) (corev1.Container, error) {
	index := strings.LastIndex(c.Image, ":")
	if index < 0 {
		return c, fmt.Errorf("wrong image format: %q", c.Image)
	}
	repo, tag := c.Image[:index], c.Image[index+1:]
	containerName := strcase.ToLowerCamel(c.Name)
	c.Image = fmt.Sprintf("{{ .Values.%[1]s.%[2]s.image.repository }}:{{ .Values.%[1]s.%[2]s.image.tag | default .Chart.AppVersion }}", name, containerName)

	err := unstructured.SetNestedField(*values, repo, name, containerName, "image", "repository")
	if err != nil {
		return c, fmt.Errorf("%w: unable to set deployment value field", err)
	}
	err = unstructured.SetNestedField(*values, tag, name, containerName, "image", "tag")
	if err != nil {
		return c, fmt.Errorf("%w: unable to set deployment value field", err)
	}

	c, err = processEnv(name, appMeta, c, values)
	if err != nil {
		return c, err
	}

	for _, e := range c.EnvFrom {
		if e.SecretRef != nil {
			e.SecretRef.Name = appMeta.TemplatedName(e.SecretRef.Name)
		}
		if e.ConfigMapRef != nil {
			e.ConfigMapRef.Name = appMeta.TemplatedName(e.ConfigMapRef.Name)
		}
	}
	c.Env = append(c.Env, corev1.EnvVar{
		Name:  cluster.DomainEnv,
		Value: fmt.Sprintf("{{ quote .Values.%s }}", cluster.DomainKey),
	})
	for k, v := range c.Resources.Requests {
		err = unstructured.SetNestedField(*values, v.ToUnstructured(), name, containerName, "resources", "requests", k.String())
		if err != nil {
			return c, fmt.Errorf("%w: unable to set container resources value", err)
		}
	}
	for k, v := range c.Resources.Limits {
		err = unstructured.SetNestedField(*values, v.ToUnstructured(), name, containerName, "resources", "limits", k.String())
		if err != nil {
			return c, fmt.Errorf("%w: unable to set container resources value", err)
		}
	}

	if c.ImagePullPolicy != "" {
		err = unstructured.SetNestedField(*values, string(c.ImagePullPolicy), name, containerName, "imagePullPolicy")
		if err != nil {
			return c, fmt.Errorf("%w: unable to set container imagePullPolicy", err)
		}
		c.ImagePullPolicy = corev1.PullPolicy(fmt.Sprintf(imagePullPolicyTemplate, name, containerName))
	}
	return c, nil
}

func processEnv(name string, appMeta helmify.AppMetadata, c corev1.Container, values *helmify.Values) (corev1.Container, error) {
	containerName := strcase.ToLowerCamel(c.Name)
	for i := 0; i < len(c.Env); i++ {
		if c.Env[i].ValueFrom != nil {
			switch {
			case c.Env[i].ValueFrom.SecretKeyRef != nil:
				c.Env[i].ValueFrom.SecretKeyRef.Name = appMeta.TemplatedName(c.Env[i].ValueFrom.SecretKeyRef.Name)
			case c.Env[i].ValueFrom.ConfigMapKeyRef != nil:
				c.Env[i].ValueFrom.ConfigMapKeyRef.Name = appMeta.TemplatedName(c.Env[i].ValueFrom.ConfigMapKeyRef.Name)
			case c.Env[i].ValueFrom.FieldRef != nil, c.Env[i].ValueFrom.ResourceFieldRef != nil:
				// nothing to change here, keep the original value
			}
			continue
		}

		err := unstructured.SetNestedField(*values, c.Env[i].Value, name, containerName, "env", strcase.ToLowerCamel(strings.ToLower(c.Env[i].Name)))
		if err != nil {
			return c, fmt.Errorf("%w: unable to set deployment value field", err)
		}
		c.Env[i].Value = fmt.Sprintf(envValue, name, containerName, "env", strcase.ToLowerCamel(strings.ToLower(c.Env[i].Name)))
	}
	return c, nil
}
