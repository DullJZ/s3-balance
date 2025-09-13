{{/*
Labels
*/}}
{{- define "s3-balance.labels" -}}
helm.sh/chart: {{ include "s3-balance.chart" . }}
{{ include "s3-balance.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels
*/}}
{{- define "s3-balance.selectorLabels" -}}
app.kubernetes.io/name: {{ include "s3-balance.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Chart name and version as used by the chart label.
*/}}
{{- define "s3-balance.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end -}}