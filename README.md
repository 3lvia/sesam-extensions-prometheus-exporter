# sesam-extensions-prometheus-exporter
Collect metrics from sesam api. This microservice is running in Elvia AKS under sesam-extensions. 

## Metrics
This microservice is creating these metrics.

| .  | Description|Type| Tags|
|:---|------------|----|----:|
|api_up|Sesam API path status|Counter|"host", "path", "status"|
|pipe_storage_mb|pipe storage (MB)|Gauge|"host", "pipe", "configGroup"|
|pipe_queue_total|pipe queue size|Gauge|"host", "pipe", "configGroup"|
|pipe_status_total|pipe status|Counter|"host", "pipe", "status", "configGroup"|
|dataset_deleted_total|total deleted entities in the dataset index|Gauge|"host", "pipe"|
|dataset_withdeleted_total|total entities in the dataset index|Gauge|"host", "pipe"|
|dataset_existed_total|total existed in the dataset log|Gauge|"host", "pipe"|

## Running the software locally

1. Setup the variables. There are three approaches to setup the necessary variables for this application.
    * Getting variables from vault.
        ```
        export VAULT_ADDR="https://{{vault_url}}"
        export GITHUB_TOKEN="{{github_token}}"
        ```
    * Read variables from the config file. The config file must follow this json structure.
        ```
        {
            "sesamConfig": {
                "host": "{{host for the API}}",
                "desc": "{{host description}}",
                "jwt": "{{token}}"
            }
        }
        ```
    * Setup the variables in your local env.
        ```
        export SESAM_HOST=...
        export HOST_DESC=...
        export HOST_JWT=...
        ```
2.  Running the code from the source code
    ```
    go run . -config.vault
    ```
## CI/CD
Azure DevOps. https://dev.azure.com/3lvia/sesam-extensions/_build?definitionId=953&_a=summary
