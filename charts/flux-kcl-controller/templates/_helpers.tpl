{{/*
Expand the name of the chart.
*/}}
{{- define "flux-kcl-controller.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name.
*/}}
{{- define "flux-kcl-controller.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "flux-kcl-controller.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "flux-kcl-controller.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: flux
{{- end }}

{{/*
Selector labels. These must not change between upgrades.
*/}}
{{- define "flux-kcl-controller.selectorLabels" -}}
app.kubernetes.io/name: {{ include "flux-kcl-controller.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Name of the service account to use.
*/}}
{{- define "flux-kcl-controller.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "flux-kcl-controller.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Container image reference.
*/}}
{{- define "flux-kcl-controller.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- with .Values.image.digest -}}
{{- printf "%s@%s" $.Values.image.repository . -}}
{{- else -}}
{{- printf "%s:%s" $.Values.image.repository $tag | quote -}}
{{- end -}}
{{- end }}

{{/*
Port the metrics endpoint listens on, or an empty string when metrics are
disabled. The controller takes an address rather than a port, and any address
without a port (e.g. "0" or "none") disables the endpoint.
*/}}
{{- define "flux-kcl-controller.metricsPort" -}}
{{- $addr := .Values.service.metrics.bindAddress | toString -}}
{{- if regexMatch ":[0-9]+$" $addr -}}
{{- $addr | splitList ":" | last -}}
{{- end -}}
{{- end }}

{{/*
Validate the values that must not be empty, so a broken release fails at render
time rather than at apply time.
*/}}
{{- define "flux-kcl-controller.validateValues" -}}
{{- if and .Values.rbac.create (not .Values.rbac.clusterAdminBinding.create) (not .Values.rbac.extraRules) }}
{{- fail "flux-kcl-controller: with rbac.create=true and rbac.clusterAdminBinding.create=false, add permissions via rbac.extraRules; the controller applies manifests for KCLRun authors and will otherwise be denied." }}
{{- end }}
{{- if and (not .Values.rbac.create) .Values.serviceAccount.create (eq (include "flux-kcl-controller.serviceAccountName" .) (include "flux-kcl-controller.fullname" .)) }}
{{- fail "flux-kcl-controller: rbac.create=false but no existing service account was named; the controller would run with no permissions." }}
{{- end }}
{{- end }}
