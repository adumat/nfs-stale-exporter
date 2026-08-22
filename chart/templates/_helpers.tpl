{{- define "nfs-stale-exporter.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "nfs-stale-exporter.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "nfs-stale-exporter.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "nfs-stale-exporter.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "nfs-stale-exporter.selectorLabels" -}}
app.kubernetes.io/name: {{ include "nfs-stale-exporter.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
kube-state-metrics exposes pod labels as label_<sanitised>, replacing every
non-alphanumeric character with an underscore. Deriving it here rather than
asking the user for it removes the single most damaging misconfiguration:
joining on the raw label name yields app="" for every series, and a KEDA query
filtering on app then matches nothing and never recovers anything.
*/}}
{{- define "nfs-stale-exporter.ksmAppLabel" -}}
{{- printf "label_%s" (regexReplaceAll "[^a-zA-Z0-9]" .Values.prometheusRule.appLabel "_") -}}
{{- end -}}

{{- define "nfs-stale-exporter.staleJoin" -}}
label_replace(
    label_replace(nfs_mount_stale, "uid", "$1", "pod_uid", "(.+)")
  * on (uid) group_left (namespace, pod) kube_pod_info
  * on (namespace, pod) group_left ({{ include "nfs-stale-exporter.ksmAppLabel" . }}) kube_pod_labels,
  "app", "$1", "{{ include "nfs-stale-exporter.ksmAppLabel" . }}", "(.+)")
{{- end -}}
