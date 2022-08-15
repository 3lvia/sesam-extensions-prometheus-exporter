package main

import (
  "github.com/prometheus/client_golang/prometheus"
  "github.com/prometheus/client_golang/prometheus/promhttp"
  "fmt"
  "flag"
  "io/ioutil"
  "net/http"
  "os"
  "time"
  "log"
  "encoding/json"
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
      }`json:"metadata"`
    }`json:"original"`
  }`json:"config"`
}

type DatasetState struct {
  Id string `json:"_id"`
  Runtime struct {
    Deleted float64 `json:"count-index-deleted"`
    WithDeleted float64 `json:"count-index-exists"`
    Existed float64 `json:"count-log-exists"`
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
  ch <- node_storage_total_mb
  ch <- pipe_storage_mb
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
  url := fmt.Sprintf("https://%s/api/%s", e.host, relativeUrl)
  log.Printf("scraping %s", url)
  req, err := http.NewRequest(http.MethodGet, url, nil)
  if err != nil {
    log.Fatalf("Error to create Request. %+v", err)
  }
  req.Header.Add("Accept", "application/json")
  req.Header.Add("Authorization", "bearer "+e.jwt)
  resp, err := client.Do(req)
  if err != nil {
    log.Fatalf("Error to get /pipes. %+v", err)
  }

  defer resp.Body.Close()

  body, err := ioutil.ReadAll(resp.Body)
  if err != nil {
    log.Fatalf("Error to parse response body. %+v", err)
  }

  if resp.StatusCode != http.StatusOK {
    log.Printf("Request failed. %+v", string(body))
    time.Sleep(1 * time.Second)
    return e.HttpGet(relativeUrl, client, retryCount+1)
  }
  return body
}

func (e *Exporter) PipesState(client *http.Client, ch chan<- prometheus.Metric) {
  relativeUrl := "pipes"
  var pipes []PipeState
  json.Unmarshal(e.HttpGet(relativeUrl, client, 0), &pipes)
  total := 0.0
  for _, pipe := range pipes {
    volumn := pipe.Storage/1024.0/1024.0
    total += volumn
    if pipe.Config.Original.Metadata.ConfigGroup == "" {
      pipe.Config.Original.Metadata.ConfigGroup = "default"
    } else if pipe.Config.Original.Metadata.ConfigGroup != "maintenance" && pipe.Config.Original.Metadata.ConfigGroup == "kafka" {
      pipe.Config.Original.Metadata.ConfigGroup = "private"
    }
    ch <- prometheus.MustNewConstMetric(
      pipe_storage_mb, prometheus.GaugeValue, volumn, e.host, pipe.Id, pipe.Config.Original.Metadata.ConfigGroup,
    )
  }
  ch <- prometheus.MustNewConstMetric(
    node_storage_total_mb, prometheus.GaugeValue, total, e.host,
  )
  close(ch)
}

func (e *Exporter) DatasetsState(client *http.Client, ch chan<-prometheus.Metric) {
  url := fmt.Sprintf("datasets?include-internal-datasets=false")
  var datasetStates []DatasetState
  json.Unmarshal(e.HttpGet(url, client, 0), &datasetStates)
  for _, datasetState := range datasetStates {
    ch <- prometheus.MustNewConstMetric(
      output_undeleted_total, prometheus.GaugeValue, datasetState.Runtime.WithDeleted-datasetState.Runtime.Deleted, e.host, datasetState.Id,
    )
    ch <- prometheus.MustNewConstMetric(
      output_deleted_total, prometheus.GaugeValue, datasetState.Runtime.Deleted, e.host, datasetState.Id,
    )
    ch <- prometheus.MustNewConstMetric(
      output_withdeleted_total, prometheus.GaugeValue, datasetState.Runtime.WithDeleted, e.host, datasetState.Id,
    )
    ch <- prometheus.MustNewConstMetric(
      output_existed_total, prometheus.GaugeValue, datasetState.Runtime.Existed, e.host, datasetState.Id,
    )
  }
  close(ch)
}

var (
  config ExporterConfig

  namespace = "sesam"
  metricsPath = "/metrics"

  node_storage_total_mb = prometheus.NewDesc(
    prometheus.BuildFQName(namespace, "",  "node_storage_total_mb"),
    "total storage (MB)", []string{"host"}, nil,
  )
  pipe_storage_mb = prometheus.NewDesc(
    prometheus.BuildFQName(namespace, "",  "pipe_storage_mb"),
    "pipe storage (MB)", []string{"host", "pipe", "configGroup"}, nil,
  )
  output_deleted_total = prometheus.NewDesc(
    prometheus.BuildFQName(namespace, "",  "output_deleted_total"),
    "total deleted entities in the output index", []string{"host", "pipe"}, nil,
  )
  output_undeleted_total = prometheus.NewDesc(
    prometheus.BuildFQName(namespace, "",  "output_undeleted_total"),
    "total undeleted entities in the output index", []string{"host", "pipe"}, nil,
  )
  output_withdeleted_total = prometheus.NewDesc(
    prometheus.BuildFQName(namespace, "",  "output_withdeleted_total"),
    "total entities in the output index", []string{"host", "pipe"}, nil,
  )
  output_existed_total = prometheus.NewDesc(
    prometheus.BuildFQName(namespace, "",  "output_existed_total"),
    "total existed in the output log", []string{"host", "pipe"}, nil,
  )
)

func main() {
//  sesamconfig := Sesam {
//    Host: "datahub-bc455335.sesam.cloud",
//    Desc: "FengPrivate",
//    Jwt: "eyJ0eXAiOiJKV1QiLCJhbGciOiJSUzI1NiJ9.eyJpYXQiOjE2NTk0MzcyOTAuMTA1MTM4NSwiZXhwIjoxNjkwOTczMjA2LCJ1c2VyX2lkIjoiNDU4NTEyYTctNmMzMi00OWU1LTkyNTUtYzNkNWQzMDFhOGFhIiwidXNlcl9wcm9maWxlIjp7ImVtYWlsIjoiZmVuZy5sdWFuQGVsdmlhLm5vIiwibmFtZSI6ImZlbmcubHVhbkBlbHZpYS5ubyIsInBpY3R1cmUiOiJodHRwczovL3MuZ3JhdmF0YXIuY29tL2F2YXRhci9lY2I3Yjg1YTk1NDM1YzNlNTQyNDIyMTA0YjgyYjdkYz9zPTQ4MCZyPXBnJmQ9aHR0cHMlM0ElMkYlMkZjZG4uYXV0aDAuY29tJTJGYXZhdGFycyUyRmZsLnBuZyJ9LCJ1c2VyX3ByaW5jaXBhbCI6Imdyb3VwOkV2ZXJ5b25lIiwicHJpbmNpcGFscyI6eyJiYzQ1NTMzNS05OTllLTRjMWUtYTQxYi0xOTY0ZmVhYjMyYjQiOlsiZ3JvdXA6QWRtaW4iXX0sImFwaV90b2tlbl9pZCI6IjA4OWY1NDdjLTc0YzktNDI0MS1iZTIyLTNkNGM2MmY3NDFmMCJ9.U1xzZZ_EZ5kekQE9LpGAdRb5H9h9msiI2hJHfnqHGKrOk6CdKij2Jwd2l7Uao9KPR1uxCRqJqK6qxt7F5fHN6xPExw8hva-9NOBe_4jpOhAtS_ifSbNFrdvgcIUawpQeDheqYLoCXLd016MLjjfTrljdpPZejSfnRcgQ7_jIVxwm1R8xhh14Fbl5l269UaITI-YP8BT-gFNHWYfEYaNhmxDUEehE8QFrLegwMtq2fxtUT7gaG4NTHK4c4KQggiWmsxFGmMZ68DisdygWnbifh-UHRHmvcqLmuDqPzwObKgh_B7SB8OCCtDJG2y2eGdI5HsmMtt351JjiEmkSRYGkPw",
//  }
  var configFile string
  flag.StringVar(&configFile, "config.file_path", "", "Path to environment file")
  flag.Parse()
  var b []byte
  b, err :=ioutil.ReadFile(configFile)
  if err != nil {
    log.Fatalf("Failed to read config file: %s", err)
    os.Exit(1)
  }
  if err := json.Unmarshal(b, &config); err != nil {
    log.Fatalf("Invalid config file: %s", err)
    os.Exit(1)
  }

  fmt.Printf("start with %s(%s)\n", config.SesamConfig.Desc, config.SesamConfig.Host)

  exporter := NewExport(config)
  prometheus.MustRegister(exporter)

  http.Handle("/metrics", promhttp.Handler())
  http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    w.Write([]byte(`
      <html>
      <head><title>Elvia Prometheus Sesam Exporter</title></head>
      <body>
      <h1>Sesam Exporter</h1>
      <p><a href='` + metricsPath + `'>Metrics</a></p>
      </body>
      </html>`))
  })
  log.Fatal(http.ListenAndServe(":8080", nil))
}
