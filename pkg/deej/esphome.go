package deej

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/omriharel/deej/pkg/deej/util"
)

// ESPHome provides a deej-aware abstraction layer to managing ESPHome I/O
type ESPHome struct {
	deej   *Deej
	logger *zap.SugaredLogger

	stopChannel chan bool
	connected   bool

	lastKnownNumSliders        int
	currentSliderPercentValues []float32

	lastGoodResults []int

	sliderMoveConsumers []chan SliderMoveEvent
}

// ESPHome sensor data
type SensorData struct {
	Id    string `json:"id"`
	State string `json:"state"`
	Value int    `json:"value"`
}

// NewESPHome creates a ESPHome instance that uses the provided deej
// instance's connection info to establish communications with the ESPHome device
func NewESPHome(deej *Deej, logger *zap.SugaredLogger) (*ESPHome, error) {
	logger = logger.Named("esphome")

	esphome := &ESPHome{
		deej:                deej,
		logger:              logger,
		stopChannel:         make(chan bool),
		connected:           false,
		sliderMoveConsumers: []chan SliderMoveEvent{},
	}

	logger.Debug("Created ESPHome instance")

	// respond to config changes
	esphome.setupOnConfigReload()

	return esphome, nil
}

// Start attempts to connect to our arduino chip
func (esphome *ESPHome) Start() error {

	// don't allow multiple concurrent connections
	if esphome.connected {
		esphome.logger.Warn("Already connected, can't start another without closing first")
		return errors.New("ESPHome: connection already active")
	}

	namedLogger := esphome.logger.Named(strings.ToLower(esphome.deej.config.ESPHomeIpAddr))

	// read lines or await a stop
	go func() {
		lineChannel := esphome.readLine(namedLogger)

		for {
			select {
			case <-esphome.stopChannel:
				esphome.close(namedLogger)
			case line := <-lineChannel:
				esphome.handleLine(namedLogger, line)
			}
		}
	}()

	return nil
}

// Stop signals us to shut down our serial connection, if one is active
func (esphome *ESPHome) Stop() {
	esphome.stopChannel <- true
}

// SubscribeToSliderMoveEvents returns an unbuffered channel that receives
// a sliderMoveEvent struct every time a slider moves
func (esphome *ESPHome) SubscribeToSliderMoveEvents() chan SliderMoveEvent {
	ch := make(chan SliderMoveEvent)
	esphome.sliderMoveConsumers = append(esphome.sliderMoveConsumers, ch)

	return ch
}

func (esphome *ESPHome) setupOnConfigReload() {
	configReloadedChannel := esphome.deej.config.SubscribeToChanges()

	const stopDelay = 50 * time.Millisecond

	go func() {
		for {
			select {
			case <-configReloadedChannel:

				// make any config reload unset our slider number to ensure process volumes are being re-set
				// (the next read line will emit SliderMoveEvent instances for all sliders)\
				// this needs to happen after a small delay, because the session map will also re-acquire sessions
				// whenever the config file is reloaded, and we don't want it to receive these move events while the map
				// is still cleared. this is kind of ugly, but shouldn't cause any issues
				go func() {
					<-time.After(stopDelay)
					esphome.lastKnownNumSliders = 0
				}()
			}
		}
	}()
}

func (esphome *ESPHome) close(logger *zap.SugaredLogger) {
	logger.Debug("ESPHome connection closed")
	esphome.connected = false
}

func (esphome *ESPHome) getLine(logger *zap.SugaredLogger) ([]int, error) {
	results := make([]int, len(esphome.deej.config.ESPHomeSliderNames))

	for index, sensorEntityID := range esphome.deej.config.ESPHomeSliderNames {
		// Construct the URL to fetch sensor data
		url := fmt.Sprintf("http://%s/sensor/%s", esphome.deej.config.ESPHomeIpAddr, sensorEntityID)

		// Send HTTP GET request to ESPHome device
		response, err := http.Get(url)
		if err != nil {
			logger.Warnw("Error fetching data:", "error", err)
			return nil, err
		}
		defer response.Body.Close()

		// Read response body
		body, err := io.ReadAll(response.Body)
		if err != nil {
			logger.Warnw("Error reading response body:", "error", err)
			return nil, err
		}

		// Parse JSON response into SensorData struct
		var sensorData SensorData
		err = json.Unmarshal(body, &sensorData)
		if err != nil {
			logger.Warnw("Error parsing JSON:", "error", err)
			return nil, err
		}

		results[index] = sensorData.Value
	}
	esphome.lastGoodResults = results
	return results, nil
}

func (esphome *ESPHome) readLine(logger *zap.SugaredLogger) chan []int {
	ch := make(chan []int)

	go func() {
		for {
			results, err := esphome.getLine(logger)
			if err != nil {

				if esphome.deej.Verbose() {
					logger.Warnw("Failed to read results from server", "error", err, "results", results)
				}

				// just ignore the results and return the last good results
				ch <- esphome.lastGoodResults
			}

			if esphome.deej.Verbose() {
				logger.Debugw("Read new results", "results", results)
			}

			// deliver the results to the channel
			ch <- results
		}
	}()

	return ch
}

func (esphome *ESPHome) handleLine(logger *zap.SugaredLogger, results []int) {
	numSliders := len(results)

	// update our slider count, if needed - this will send slider move events for all
	if numSliders != esphome.lastKnownNumSliders {
		logger.Infow("Detected sliders", "amount", numSliders)
		esphome.lastKnownNumSliders = numSliders
		esphome.currentSliderPercentValues = make([]float32, numSliders)

		// reset everything to be an impossible value to force the slider move event later
		for idx := range esphome.currentSliderPercentValues {
			esphome.currentSliderPercentValues[idx] = -1.0
		}
	}

	// for each slider:
	moveEvents := []SliderMoveEvent{}
	for sliderIdx, number := range results {
		// turns out the first line could come out dirty sometimes (i.e. "4558|925|41|643|220")
		// so let's check the first number for correctness just in case
		if sliderIdx == 0 && number > 1023 {
			esphome.logger.Debugw("Got malformed line from serial, ignoring", "results", results)
			return
		}

		// map the value from raw to a "dirty" float between 0 and 1 (e.g. 0.15451...)
		dirtyFloat := float32(number) / 1023.0

		// normalize it to an actual volume scalar between 0.0 and 1.0 with 2 points of precision
		normalizedScalar := util.NormalizeScalar(dirtyFloat)

		// if sliders are inverted, take the complement of 1.0
		if esphome.deej.config.InvertSliders {
			normalizedScalar = 1 - normalizedScalar
		}

		// check if it changes the desired state (could just be a jumpy raw slider value)
		if util.SignificantlyDifferent(esphome.currentSliderPercentValues[sliderIdx], normalizedScalar, esphome.deej.config.NoiseReductionLevel) {

			// if it does, update the saved value and create a move event
			esphome.currentSliderPercentValues[sliderIdx] = normalizedScalar

			moveEvents = append(moveEvents, SliderMoveEvent{
				SliderID:     sliderIdx,
				PercentValue: normalizedScalar,
			})

			if esphome.deej.Verbose() {
				logger.Debugw("Slider moved", "event", moveEvents[len(moveEvents)-1])
			}
		}
	}

	// deliver move events if there are any, towards all potential consumers
	if len(moveEvents) > 0 {
		for _, consumer := range esphome.sliderMoveConsumers {
			for _, moveEvent := range moveEvents {
				consumer <- moveEvent
			}
		}
	}
}
