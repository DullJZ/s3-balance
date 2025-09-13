{{/*
Return the proper s3-balance image name
*/}}
{{- define "s3-balance.image" -}}
{{- $registryName := .Values.image.repository -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- printf "%s:%s" $registryName $tag -}}
{{- end -}}

{{/*
Return the chart name
*/}}
{{- define "s3-balance.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Return the full name  
*/}}
{{- define "s3-balance.fullname" -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/*
Return the service account name
*/}}
{{- define "s3-balance.serviceAccountName" -}}
{{- default (include "s3-balance.fullname" .) .Values.serviceAccount.name -}}
{{- end -}}