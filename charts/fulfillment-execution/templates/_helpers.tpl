{{- define "fulfillment-execution.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "fulfillment-execution.fullname" -}}
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

{{- define "fulfillment-execution.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "fulfillment-execution.labels" -}}
helm.sh/chart: {{ include "fulfillment-execution.chart" . }}
{{ include "fulfillment-execution.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "fulfillment-execution.selectorLabels" -}}
app.kubernetes.io/name: {{ include "fulfillment-execution.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "fulfillment-execution.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "fulfillment-execution.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "fulfillment-execution.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- include "fulfillment-execution.fullname" . }}-database
{{- end }}
{{- end }}

{{- define "fulfillment-execution.analyticsSecretName" -}}
{{- if .Values.analytics.database.existingSecret }}
{{- .Values.analytics.database.existingSecret }}
{{- else }}
{{- include "fulfillment-execution.fullname" . }}-analytics-database
{{- end }}
{{- end }}

{{- define "fulfillment-execution.projectorFullname" -}}
{{- printf "%s-projector" (include "fulfillment-execution.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "fulfillment-execution.reportsFullname" -}}
{{- printf "%s-reports" (include "fulfillment-execution.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "fulfillment-execution.mcpFullname" -}}
{{- printf "%s-mcp" (include "fulfillment-execution.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "fulfillment-execution.mcpSecretName" -}}
{{- if .Values.mcp.existingSecret }}
{{- .Values.mcp.existingSecret }}
{{- else }}
{{- printf "%s-mcp-keys" (include "fulfillment-execution.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "fulfillment-execution.authSecretName" -}}
{{- if .Values.auth.existingSecret }}
{{- .Values.auth.existingSecret }}
{{- else }}
{{- printf "%s-auth" (include "fulfillment-execution.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- /*
authEnv renders the REST-identity env entries (ADR-0021): AUTH_MODE from the
ConfigMap plus API_READ_KEY / API_READWRITE_KEY from the auth Secret. Each
secret ref is emitted only when that key is configured (or an existingSecret
is named, in which case both are assumed present), so a pod never references
a Secret data key that was not rendered.
*/ -}}
{{- define "fulfillment-execution.authEnv" -}}
- name: AUTH_MODE
  valueFrom:
    configMapKeyRef:
      name: {{ include "fulfillment-execution.fullname" . }}
      key: AUTH_MODE
{{- if or .Values.auth.readKey .Values.auth.existingSecret }}
- name: API_READ_KEY
  valueFrom:
    secretKeyRef:
      name: {{ include "fulfillment-execution.authSecretName" . }}
      key: API_READ_KEY
{{- end }}
{{- if or .Values.auth.readWriteKey .Values.auth.existingSecret }}
- name: API_READWRITE_KEY
  valueFrom:
    secretKeyRef:
      name: {{ include "fulfillment-execution.authSecretName" . }}
      key: API_READWRITE_KEY
{{- end }}
{{- end }}
