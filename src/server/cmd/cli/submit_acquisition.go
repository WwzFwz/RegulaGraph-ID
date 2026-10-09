// Loads a bounded collector record for submit's optional production PDF import.
// CLI decoding rejects unknown/trailing input; workflow owns provenance, resource
// limits and storage ordering. It performs no network fetch or canonical inference.
// Measure record decode/import latency separately from worker time; required
// benchmark targets remain in configs/benchmark-targets.yaml, unmeasured here.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"google.golang.org/protobuf/encoding/protojson"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/ingestion/sources"
	"regulagraph.local/server/internal/workflows"
)

func prepareSubmitAcquisition(raw []byte, recordPath string) (*workflows.AcquisitionImport, error) {
	request := new(pb.IngestionRequest)
	if err := protojson.Unmarshal(raw, request); err != nil {
		return nil, err
	}
	data, err := readBoundedSubmitRequest(recordPath)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record sources.Record
	if err = decoder.Decode(&record); err != nil {
		return nil, err
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("collector record must contain one JSON object")
	}
	return workflows.PrepareAcquisitionImport(request, record)
}
