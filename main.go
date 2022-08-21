package main

import (
  "github.com/prometheus/client_golang/prometheus"
  "github.com/prometheus/client_golang/prometheus/promhttp"
  "github.com/3lvia/hn-config-lib-go/vault"
  "fmt"
  "flag"
  "io/ioutil"
  "net/http"
  "os"
  "time"
  "log"
  "encoding/json"
  "strings"
)

type ExporterConfig struct {
  SesamConfig struct {
    Host string `json:"host"`
    Desc string `json:"desc"`
    Jwt string `json:"jwt"`
  }
}

func httpClient() *http.Client {
  client := &http.Client{
    Transport: &http.Transport{
      MaxIdleConnsPerHost: 60,
      MaxConnsPerHost: 50,
    },
    Timeout: 60 * time.Second,
  }

  return client
}

type PipeState struct {
  Id string `json:"_id"`
  Storage float64 `json:"storage"`
  Config struct {
    Original struct {
      Metadata struct {
        ConfigGroup string `json:"$config-group"`
        Durable bool `json:"durable"`
      }`json:"metadata"`
    }`json:"original"`
  }`json:"config"`
  Runtime struct {
    Queues struct {
      Source interface{} `json:"source"`
      Dependencies map[string]float64 `json:"dependencies"`
    }`json:"queues"`
    LastStarted string`json:"last-started"`
    LastRun string `json:"last-run"`
    NextRun string `json:"next-run"`
    AverageProcessTime float64 `json:"average-process-time"`
    State string `json:"state"`
    Success *bool `json:"success"`
    DeadletterDataset string `json:"dead-letter-dataset"`
    // last-seen can be int, string, datetime
    LastSeen interface{} `json:"last-seen"`
    // restore_uuid can be a dict or string
    RestoreUuid interface{} `json:"restore_uuid"`
  }`json:"runtime"`
}

type DatasetState struct {
  Id string `json:"_id"`
  Runtime struct {
    LastModified time.Time `json:"last-modified"`
    Deleted float64 `json:"count-index-deleted"`
    WithDeleted float64 `json:"count-index-exists"`
    Existed float64 `json:"count-log-exists"`
    HasCircuitBreaker bool `json:"has-circuit-breaker"`
  }
}

type Exporter struct {
  host, jwt, description string
}

// a factory method for Exporter
func NewExport(config ExporterConfig) *Exporter {
  if config.SesamConfig.Host == "" {
    log.Fatal("Missing variable 'host'")
  }
  if config.SesamConfig.Desc == "" {
    log.Fatal("Missing variable 'desc'")
  }

  return &Exporter{
    host: config.SesamConfig.Host,
    description: config.SesamConfig.Desc,
    jwt: config.SesamConfig.Jwt,
  }
}

//send the description of the defined metrics
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
//   ch <- node_storage_total_mb
  ch <- pipe_storage_mb
  ch <- pipe_queue_total
  ch <- pipe_status_total
  ch <- dataset_deleted_total
  ch <- dataset_existed_total
  ch <- dataset_withdeleted_total
}

func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
  client := httpClient()
  pipeCh := make(chan prometheus.Metric)
  datasetCh := make(chan prometheus.Metric)
  go e.PipesState(client, pipeCh)
  go e.DatasetsState(client, datasetCh)
  for m := range pipeCh {
    ch <- m
  }
  for m := range datasetCh {
    ch <- m
  }
}

func (e *Exporter) HttpGet(relativeUrl string, client *http.Client, retryCount int) []byte  {
  if retryCount >= 3 {
    return nil
  }
  var url string
  if strings.HasPrefix(relativeUrl, "/") {
    url = fmt.Sprintf("https://%s/api%s", e.host, relativeUrl)
  } else {
    url = fmt.Sprintf("https://%s/api/%s", e.host, relativeUrl)
  }

  log.Printf("scraping %s", url)
  req, err := http.NewRequest(http.MethodGet, url, nil)
  if err != nil {
    log.Printf("Error to create Request. %+v", err)
  }
  req.Header.Add("Accept", "application/json")
  req.Header.Add("Authorization", "bearer "+e.jwt)
  req.Header.Add("accept-encoding", "gzip, deflate, br")
  resp, err := client.Do(req)
  if err != nil {
    log.Printf("Error to connect %s: %+v", relativeUrl, err)
    time.Sleep(1 * time.Second)
    return e.HttpGet(relativeUrl, client, retryCount+1)
  }

  defer resp.Body.Close()

  body, err := ioutil.ReadAll(resp.Body)
  if err != nil {
    log.Printf("Error to parse response body for %s: %+v", relativeUrl, err)
    time.Sleep(1 * time.Second)
    return e.HttpGet(relativeUrl, client, retryCount+1)
  }

  if resp.StatusCode != http.StatusOK {
    // log.Printf("Request failed. %+v", string(body))
    time.Sleep(1 * time.Second)
    return e.HttpGet(relativeUrl, client, retryCount+1)
  }
  return body
}

func (e *Exporter) PipesState(client *http.Client, ch chan<- prometheus.Metric) {
  relativeUrl := "pipes"
  var pipes []PipeState
  err := json.Unmarshal(e.HttpGet(relativeUrl, client, 0), &pipes)
  if err != nil {
    log.Printf("Error in parsing json from %s: %s", relativeUrl, err)
  }

  for _, pipe := range pipes {
    volumn := pipe.Storage/1024.0/1024.0
    if pipe.Config.Original.Metadata.ConfigGroup == "" {
      pipe.Config.Original.Metadata.ConfigGroup = "default"
    } else if pipe.Config.Original.Metadata.ConfigGroup != "maintenance" && pipe.Config.Original.Metadata.ConfigGroup != "kafka" {
      pipe.Config.Original.Metadata.ConfigGroup = "private"
    }
    ch <- prometheus.MustNewConstMetric(
      pipe_storage_mb, prometheus.GaugeValue, volumn, e.host, pipe.Id, pipe.Config.Original.Metadata.ConfigGroup,
    )
    var queueSize float64
    if v, ok := pipe.Runtime.Queues.Source.(float64); ok {
      queueSize += v
    } else if v, ok :=  pipe.Runtime.Queues.Source.(map[string]interface{}); ok {
      for _, value := range v {
        value, ok := value.(float64)
        if !ok {
          log.Printf("Err: Failed to read source size. %+v\n", pipe)
        }
        queueSize += value
      }
    } else {
      log.Printf("Err: Failed to read source size. %+v\n", pipe)
    }
    for _, value := range pipe.Runtime.Queues.Dependencies {
      queueSize += value
    }

    ch <- prometheus.MustNewConstMetric(
      pipe_queue_total, prometheus.GaugeValue, queueSize, e.host, pipe.Id, pipe.Config.Original.Metadata.ConfigGroup,
    )
    status := "ok"
    if pipe.Runtime.Success == nil {
      status = "ok"
    } else if *pipe.Runtime.Success == false {
      status = "failed"
    } else if pipe.Runtime.State == "running" && pipe.Runtime.NextRun != "" {
      nextRun, err := time.Parse(time.RFC3339Nano, pipe.Runtime.NextRun)
      if err != nil {
        log.Printf("Error: %s for %s", err, pipe.Id)
      }
      cTime := time.Now()
      over1h := cTime.Add(1 * time.Hour)
      over24h := cTime.Add(24 * time.Hour)
      if over24h.After(nextRun) {
        status = "over24h"
      } else if over1h.After(nextRun) {
        status = "over1h"
      }
    }

    ch <- prometheus.MustNewConstMetric(
      pipe_status_total, prometheus.CounterValue, 1.0, e.host, pipe.Id, status, pipe.Config.Original.Metadata.ConfigGroup,
    )
  }
  log.Printf("Total scraped pipes: %d\n", len(pipes))
  close(ch)
}

func (e *Exporter) DatasetsState(client *http.Client, ch chan<-prometheus.Metric) {
  relativeUrl := "datasets?include-internal-datasets=false"
  url := fmt.Sprintf(relativeUrl)
  var datasetStates []DatasetState
  err := json.Unmarshal(e.HttpGet(url, client, 0), &datasetStates)
  if err != nil {
    log.Printf("Error in parsing %s, json: %s", relativeUrl,  err)
  }
  for _, datasetState := range datasetStates {
//    ch <- prometheus.MustNewConstMetric(
//      dataset_undeleted_total, prometheus.GaugeValue, datasetState.Runtime.WithDeleted-datasetState.Runtime.Deleted, e.host, datasetState.Id,
//    )
    ch <- prometheus.MustNewConstMetric(
      dataset_deleted_total, prometheus.GaugeValue, datasetState.Runtime.Deleted, e.host, datasetState.Id,
    )
    ch <- prometheus.MustNewConstMetric(
      dataset_withdeleted_total, prometheus.GaugeValue, datasetState.Runtime.WithDeleted, e.host, datasetState.Id,
    )
    ch <- prometheus.MustNewConstMetric(
      dataset_existed_total, prometheus.GaugeValue, datasetState.Runtime.Existed, e.host, datasetState.Id,
    )
  }
  log.Printf("scraped %d datasets", len(datasetStates))
  close(ch)
}

var (
  config ExporterConfig

  namespace = "sesam"
  metricsPath = "/metrics"

//  node_storage_total_mb = prometheus.NewDesc(
//    prometheus.BuildFQName(namespace, "",  "node_storage_total_mb"),
//    "total storage (MB)", []string{"host"}, nil,
//  )
  pipe_storage_mb = prometheus.NewDesc(
    prometheus.BuildFQName(namespace, "",  "pipe_storage_mb"),
    "pipe storage (MB)", []string{"host", "pipe", "configGroup"}, nil,
  )
  pipe_queue_total = prometheus.NewDesc(
    prometheus.BuildFQName(namespace, "",  "pipe_queues_total"),
    "pipe queue size", []string{"host", "pipe", "configGroup"}, nil,
  )
  pipe_status_total = prometheus.NewDesc(
    prometheus.BuildFQName(namespace, "",  "pipe_status_total"),
    "pipe status counter", []string{"host", "pipe", "status", "configGroup"}, nil,
  )
  dataset_deleted_total = prometheus.NewDesc(
    prometheus.BuildFQName(namespace, "",  "dataset_deleted_total"),
    "total deleted entities in the output index", []string{"host", "pipe"}, nil,
  )
//  dataset_undeleted_total = prometheus.NewDesc(
//    prometheus.BuildFQName(namespace, "",  "dataset_undeleted_total"),
//    "total undeleted entities in the output index", []string{"host", "pipe"}, nil,
//  )
  dataset_withdeleted_total = prometheus.NewDesc(
    prometheus.BuildFQName(namespace, "",  "dataset_withdeleted_total"),
    "total entities in the output index", []string{"host", "pipe"}, nil,
  )
  dataset_existed_total = prometheus.NewDesc(
    prometheus.BuildFQName(namespace, "",  "dataset_existed_total"),
    "total existed in the output log", []string{"host", "pipe"}, nil,
  )
)

func main() {
  var configFile string
  flag.StringVar(&configFile, "config.file_path", "", "Path to an environment file")
  vaultv := flag.Bool("config.vault", false, "Get secrets fra vault")
  envv := flag.Bool("config.env", false, "Get the setting from env variables")
  flag.Parse()

  if configFile != "" {
    var b []byte
    b, err :=ioutil.ReadFile(configFile)
    if err != nil {
      log.Fatalf("Failed to read config file: %s", err)
    }
    if err := json.Unmarshal(b, &config); err != nil {
      log.Fatalf("Invalid config file: %s", err)
      os.Exit(1)
    }
  } else if *vaultv {
    v, err := vault.New()
    if err != nil {
      log.Fatal(err)
    }
    secretsDep := &SesamSecrets{}
    vault.RegisterDynamicSecretDependency(secretsDep, v, nil)
    config.SesamConfig.Host = secretsDep.Host()
    config.SesamConfig.Desc = secretsDep.Desc()
    config.SesamConfig.Jwt = secretsDep.Jwt()
  } else if *envv == true {
    config.SesamConfig.Host = os.Getenv("SESAM_HOST")
    config.SesamConfig.Desc = os.Getenv("HOST_DESC")
    config.SesamConfig.Jwt = os.Getenv("HOST_JWT")
  } else {
    log.Fatal("wrong arguments!")
  }

  if config.SesamConfig.Host == "" {
    log.Fatal("SESAM_HOST is not defined...")
  }
  if config.SesamConfig.Desc == "" {
    log.Fatal("HOST_Desc is not defined...")
  }
  if config.SesamConfig.Jwt == "" {
    log.Fatal("HOST_Jwt is not defined...")
  }
  log.Printf("start with %s(%s)\n", config.SesamConfig.Desc, config.SesamConfig.Host)

  exporter := NewExport(config)
  prometheus.MustRegister(exporter)

  http.Handle("/metrics", promhttp.Handler())
  http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    w.Write([]byte(`
      <html>
      <head><title>Elvia Prometheus Sesam Exporter</title></head>
      <body>
      <h1>Prometheus Exporter for Sesam (` + config.SesamConfig.Host + ` -- ` + config.SesamConfig.Desc + `)</h1>
      <p><a href='` + metricsPath + `'>Metrics</a></p>
      </body>
      </html>`))
  })
  log.Fatal(http.ListenAndServe(":8080", nil))
}
