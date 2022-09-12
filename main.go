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
  "strconv"
)

const inteval = 30 * time.Second

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
    Origin string `json:"origin"`
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
    Origin string `json:"origin"`
  }`json:"runtime"`
}

func startScrape() {
  client := httpClient()
  var pipeStates []PipeState
  var datasetStates []DatasetState
  for {
    pipeChan := make(chan PipeState)
    datasetChan := make(chan DatasetState)
    go PipesState(client, pipeStates, pipeChan)
    go DatasetsState(client, datasetStates, datasetChan)
    pipeStates = nil
    datasetStates = nil
    for ps := range pipeChan{
      pipeStates = append(pipeStates, ps)
    }
    for ds := range datasetChan{
      datasetStates = append(datasetStates, ds)
    }
    time.Sleep(inteval)
  }
}

func HttpGet(relativeUrl string, client *http.Client, retryCount int) []byte  {
  if retryCount >= 3 {
    api_up.WithLabelValues(config.SesamConfig.Host, relativeUrl, "error").Inc()
    return nil
  }
  var url string
  if strings.HasPrefix(relativeUrl, "/") {
    url = fmt.Sprintf("https://%s/api%s", config.SesamConfig.Host, relativeUrl)
  } else {
    url = fmt.Sprintf("https://%s/api/%s",config.SesamConfig.Host, relativeUrl)
  }

  log.Printf("scraping %s", url)
  req, err := http.NewRequest(http.MethodGet, url, nil)
  if err != nil {
    log.Printf("Error to create Request. %+v", err)
  }
  req.Header.Add("Accept", "application/json")
  req.Header.Add("Authorization", "bearer "+config.SesamConfig.Jwt)
  req.Header.Add("accept-encoding", "gzip, deflate, br")
  resp, err := client.Do(req)
  if err != nil {
    log.Printf("Error to connect %s: %+v", relativeUrl, err)
    time.Sleep(1 * time.Second)
    return HttpGet(relativeUrl, client, retryCount+1)
  }

  defer resp.Body.Close()

  body, err := ioutil.ReadAll(resp.Body)
  if err != nil {
    log.Printf("Error to parse response body for %s: %+v", relativeUrl, err)
    time.Sleep(2 * time.Second)
    HttpGet(relativeUrl, client, retryCount+1)
  }

  api_up.WithLabelValues(config.SesamConfig.Host, relativeUrl, strconv.Itoa(resp.StatusCode)).Inc()
  if resp.StatusCode != http.StatusOK {
    // log.Printf("Request failed. %+v", string(body))
    time.Sleep(2 * time.Second)
    HttpGet(relativeUrl, client, retryCount+1)
  }
  return body
}

func PipesState(client *http.Client, oldStates []PipeState, ch chan PipeState) {
  relativeUrl := "pipes"
  var pipes []PipeState
  err := json.Unmarshal(HttpGet(relativeUrl, client, 0), &pipes)
  if err != nil {
    log.Printf("Error in parsing json from %s: %s", relativeUrl, err)
    api_up.WithLabelValues(config.SesamConfig.Host, relativeUrl, "error").Inc()
  }

  if len(pipes) == 0 {
    pipes = oldStates
  }

  s := 0

  for _, pipe := range pipes {
    if pipe.Runtime.Origin != "user" {
      continue
    }
    s++
    ch <- pipe
    if pipe.Config.Original.Metadata.ConfigGroup == "" {
      pipe.Config.Original.Metadata.ConfigGroup = "default"
    } else if pipe.Config.Original.Metadata.ConfigGroup != "maintenance" && pipe.Config.Original.Metadata.ConfigGroup != "kafka" {
      pipe.Config.Original.Metadata.ConfigGroup = "private"
    }
    pipe_storage_bytes.WithLabelValues(config.SesamConfig.Host, pipe.Id, pipe.Config.Original.Metadata.ConfigGroup).Set(pipe.Storage)

    var queueSize float64
    if pipe.Runtime.Queues.Source == nil {
      queueSize = 0.0
    } else  if v, ok := pipe.Runtime.Queues.Source.(float64); ok {
      queueSize = v
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
    pipe_queue_total.WithLabelValues(config.SesamConfig.Host, pipe.Id, pipe.Config.Original.Metadata.ConfigGroup).Set(queueSize)

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
      over1h := nextRun.Add(1 * time.Hour)
      over24h := nextRun.Add(24 * time.Hour)

      if cTime.After(over24h) {
        status = "over24h"
      } else if cTime.After(over1h) {
        status = "over1h"
      }
    }
    pipe_status_total.WithLabelValues(config.SesamConfig.Host, pipe.Id, status, pipe.Config.Original.Metadata.ConfigGroup).Inc()
  }
  log.Printf("scraped %d/%d user pipes...\n", s, len(pipes))
  close(ch)
}

func DatasetsState(client *http.Client, oldStates []DatasetState, ch chan DatasetState) {
  relativeUrl := "datasets"
  url := fmt.Sprintf(relativeUrl)
  var datasetStates []DatasetState
  err := json.Unmarshal(HttpGet(url, client, 0), &datasetStates)
  if err != nil {
    log.Printf("Error in parsing %s, json: %s", relativeUrl,  err)
  }

  if len(datasetStates) == 0 {
    datasetStates = oldStates
  }

  s := 0
  for _, datasetState := range datasetStates {
    if datasetState.Runtime.Origin != "user" && !strings.HasPrefix(datasetState.Id, "system:dead-letter") {
      continue
    }
    s++
    ch <- datasetState
    dataset_deleted_total.WithLabelValues(config.SesamConfig.Host, datasetState.Id).Set(datasetState.Runtime.Deleted)
    dataset_withdeleted_total.WithLabelValues(config.SesamConfig.Host, datasetState.Id).Set(datasetState.Runtime.WithDeleted)
    dataset_existed_total.WithLabelValues(config.SesamConfig.Host, datasetState.Id).Set(datasetState.Runtime.Existed)
  }
  log.Printf("scraped %d/%d datasets...", s, len(datasetStates))
  close(ch)
}

var (
  config ExporterConfig

  namespace = "sesam"
  metricsPath = "/metrics"

   api_up = prometheus.NewCounterVec(
    prometheus.CounterOpts{
      Namespace: namespace,
      Subsystem: "",
      Name: "api_up",
      Help: "Seasm API status",
    },
    []string{"host", "path", "status"},
  )
  pipe_storage_bytes = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
      Namespace: namespace,
      Subsystem: "",
      Name: "pipe_storage_bytes",
      Help: "pipe storage (bytes)",
    },
     []string{"host", "pipe", "configGroup"},
  )
  pipe_queue_total = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
      Namespace: namespace,
      Subsystem: "",
      Name: "pipe_queue_total",
      Help: "pipe queue size",
    },
    []string{"host", "pipe", "configGroup"},
  )
  pipe_status_total = prometheus.NewCounterVec(
    prometheus.CounterOpts{
      Namespace: namespace,
      Subsystem: "",
      Name: "pipe_status_total",
      Help: "pipe status counter",
    },
    []string{"host", "pipe", "status", "configGroup"},
  )
  dataset_deleted_total = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
      Namespace: namespace,
      Subsystem: "",
      Name: "dataset_deleted_total",
      Help: "total deleted entities in the dataset index",
    },
    []string{"host", "pipe"},
  )
  dataset_withdeleted_total = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
      Namespace: namespace,
      Subsystem: "",
      Name: "dataset_withdeleted_total",
      Help: "total entities in the dataset index",
    },
    []string{"host", "pipe"},
  )
  dataset_existed_total = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
      Namespace: namespace,
      Subsystem: "",
      Name: "dataset_existed_total",
      Help: "total existed in the dataset log",
    },
    []string{"host", "pipe"},
  )
)

func init() {
  prometheus.MustRegister(api_up)
  prometheus.MustRegister(pipe_storage_bytes)
  prometheus.MustRegister(pipe_queue_total)
  prometheus.MustRegister(pipe_status_total)
  prometheus.MustRegister(dataset_deleted_total)
  prometheus.MustRegister(dataset_existed_total)
  prometheus.MustRegister(dataset_withdeleted_total)
}

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
  go startScrape()

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
