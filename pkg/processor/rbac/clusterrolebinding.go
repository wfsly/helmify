package rbac

import (
	"fmt"
	"github.com/arttor/helmify/pkg/processor"
	"io"
	"strings"
	"text/template"

	"github.com/arttor/helmify/pkg/helmify"
	yamlformat "github.com/arttor/helmify/pkg/yaml"
	"github.com/pkg/errors"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var clusterRoleBindingTempl, _ = template.New("clusterRoleBinding").Parse(
	`{{ .Meta }}
{{ .RoleRef }}
{{ .Subjects }}`)

var clusterRoleBindingGVC = schema.GroupVersionKind{
	Group:   "rbac.authorization.k8s.io",
	Version: "v1",
	Kind:    "ClusterRoleBinding",
}

// ClusterRoleBinding creates processor for k8s ClusterRoleBinding resource.
func ClusterRoleBinding() helmify.Processor {
	return &clusterRoleBinding{}
}

type clusterRoleBinding struct{}

// Process k8s ClusterRoleBinding object into template. Returns false if not capable of processing given resource type.
func (r clusterRoleBinding) Process(info helmify.ChartInfo, obj *unstructured.Unstructured) (bool, helmify.Template, error) {
	if obj.GroupVersionKind() != clusterRoleBindingGVC {
		return false, nil, nil
	}

	rb := rbacv1.ClusterRoleBinding{}
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &rb)
	if err != nil {
		return true, nil, errors.Wrap(err, "unable to cast to RoleBinding")
	}

	name, meta, err := processor.ProcessMetadata(info, obj)
	if err != nil {
		return true, nil, err
	}

	fullNameTempl := fmt.Sprintf(`{{ include "%s.fullname" . }}`, info.ChartName)
	rb.RoleRef.Name = strings.ReplaceAll(rb.RoleRef.Name, info.ApplicationName, fullNameTempl)

	roleRef, err := yamlformat.Marshal(map[string]interface{}{"roleRef": &rb.RoleRef}, 0)
	if err != nil {
		return true, nil, err
	}

	for i, s := range rb.Subjects {
		s.Namespace = "{{ .Release.Namespace }}"
		s.Name = strings.ReplaceAll(s.Name, info.ApplicationName, fullNameTempl)
		rb.Subjects[i] = s
	}
	subjects, err := yamlformat.Marshal(map[string]interface{}{"subjects": &rb.Subjects}, 0)
	if err != nil {
		return true, nil, err
	}

	return true, &crbResult{
		name: name,
		data: struct {
			Meta     string
			RoleRef  string
			Subjects string
		}{
			Meta:     meta,
			RoleRef:  roleRef,
			Subjects: subjects,
		},
	}, nil
}

type crbResult struct {
	name string
	data struct {
		Meta     string
		RoleRef  string
		Subjects string
	}
}

func (r *crbResult) Filename() string {
	return strings.TrimSuffix(r.name, "-rolebinding") + "-rbac.yaml"
}

func (r *crbResult) Values() helmify.Values {
	return helmify.Values{}
}

func (r *crbResult) Write(writer io.Writer) error {
	return clusterRoleBindingTempl.Execute(writer, r.data)
}
