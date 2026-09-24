{{- define "goff-studio.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "goff-studio.fullname" -}}
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

{{- define "goff-studio.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "goff-studio.labels" -}}
helm.sh/chart: {{ include "goff-studio.chart" . }}
{{ include "goff-studio.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "goff-studio.selectorLabels" -}}
app.kubernetes.io/name: {{ include "goff-studio.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "goff-studio.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "goff-studio.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "goff-studio.configMapName" -}}
{{- if .Values.existingConfigMap }}
{{- .Values.existingConfigMap }}
{{- else }}
{{- include "goff-studio.fullname" . }}
{{- end }}
{{- end }}

{{- define "goff-studio.secretName" -}}
{{- if .Values.secrets.existingSecret }}
{{- .Values.secrets.existingSecret }}
{{- else }}
{{- include "goff-studio.fullname" . }}
{{- end }}
{{- end }}

{{- define "goff-studio.configYaml" -}}
{{- if .Values.configOverride }}
{{- .Values.configOverride }}
{{- else }}
{{- $cfg := deepCopy .Values.config }}
{{- $github := default (dict) $cfg.github }}
{{- $_ := set $github "privateKeyPath" .Values.githubAppPrivateKeyPath }}
{{- $_ := set $cfg "github" $github }}
{{- toYaml $cfg }}
{{- end }}
{{- end }}
