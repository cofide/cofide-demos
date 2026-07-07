{{- define "bank.labels" -}}
app.kubernetes.io/part-of: bank
mode: {{ .Values.authMode }}
{{- end -}}
