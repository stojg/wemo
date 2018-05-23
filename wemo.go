package wemo

import (
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/huin/goupnp"
)

func Discover() ([]*WemoSwitch, error) {
	var switches []*WemoSwitch
	devices, err := goupnp.DiscoverDevices("urn:Belkin:service:basicevent:1")
	if err != nil {
		return switches, err
	}
	if len(devices) == 0 {
		return switches, err
	}

	for i := range devices {
		var wemoSwitch WemoSwitch
		setupXml, err := getWemoInfo(devices[i].Root.URLBaseStr)
		if err != nil {
			return switches, err
		}
		var setup wemoSetupXML
		xml.Unmarshal(setupXml, &setup)
		wemoSwitch.Host = devices[i].Root.URLBase.Host
		wemoSwitch.name = setup.FriendlyName
		switches = append(switches, &wemoSwitch)
	}
	return switches, nil
}

func NewWemo(host, name string) *WemoSwitch {
	return &WemoSwitch{
		Host:        host,
		name:        name,
		lastUpdated: 0,
	}
}

type WemoSwitch struct {
	Host        string
	name        string
	lastUpdated int64
	Insight     wemoInsight
}

func (w *WemoSwitch) Name() string {
	return w.name
}

func (w *WemoSwitch) ID() string {
	return w.name
}

func (a *WemoSwitch) LastUpdated() int64 {
	return a.lastUpdated
}

func (w *WemoSwitch) State() bool {
	return w.Insight.State
}

func (w *WemoSwitch) CurrentW() float64 {
	return w.Insight.CurrentW
}

func (w *WemoSwitch) On() error {
	if err := w.setBinaryState("1"); err != nil {
		return err
	}
	w.Insight.State = true
	return nil
}

func (w *WemoSwitch) Off() error {
	if err := w.setBinaryState("0"); err != nil {
		return err
	}
	w.Insight.State = false
	return nil
}

func (s *WemoSwitch) Status() (int, error) {
	var binaryState wemoBinaryStateXML
	req, err := s.getRequest("GetBinaryState", "basicevent")
	if err != nil {
		return 0, err
	}
	client := http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	xml.Unmarshal(body, &binaryState)
	return binaryState.BinaryState, nil
}

//func (a *WemoSwitch) MarshalJSON() ([]byte, error) {
//	return json.Marshal(&state.Switch{
//		ID:         a.ID(),
//		Name:       a.Name(),
//		State:      a.State(),
//		LastChange: time.Now().Unix(),
//		CurrentW:   a.CurrentW(),
//	})
//}

type wemoInsight struct {
	XMLName           xml.Name `xml:"Envelope"`
	InsightParams     string   `xml:"Body>GetInsightParamsResponse>InsightParams"`
	State             bool
	LastChange        int
	OnSeconds         int     // Seconds it has been on since getting turned on (0 if off)
	OnSecondsToday    int     // Seconds it has been on today
	OnSecondsTwoWeeks int     // Seconds it has been on over the past two weeks.
	AverageWatt       float64 // Average power (W)
	CurrentW          float64 // Instantaneous power (W)
	EnergyToday       float64 // Energy used today in kW/Hours
	EnergyTwoWeeks    float64 // Energy used over past two weeks in mW-minutes.
}

func (i *wemoInsight) parse() {
	vars := strings.Split(i.InsightParams, "|")

	if len(vars) < 10 {
		return
	}
	if vars[0] == "1" {
		i.State = true
	}

	if val, err := strconv.Atoi(vars[1]); err == nil {
		i.LastChange = val
	}

	if val, err := strconv.Atoi(vars[2]); err == nil {
		i.OnSeconds = val
	}

	if val, err := strconv.Atoi(vars[3]); err == nil {
		i.OnSecondsToday = val
	}

	if val, err := strconv.Atoi(vars[4]); err == nil {
		i.OnSecondsTwoWeeks = val
	}

	// vars[5] Was constant between different devices I saw — always 1209600. This is two weeks in seconds. Specifies
	// the time window for average time on per day and average instantaneous power calculations.

	if val, err := strconv.Atoi(vars[6]); err == nil {
		i.AverageWatt = float64(val)
	}

	if val, err := strconv.Atoi(vars[7]); err == nil {
		i.CurrentW = float64(val) / 1000
	}

	if val, err := strconv.Atoi(vars[8]); err == nil {
		i.EnergyToday = float64(val) / 1000 / 60
	}

	if val, err := strconv.ParseFloat(vars[9], 32); err == nil {
		i.EnergyTwoWeeks = float64(val) / 1000 / 60
	}
}

func (s *WemoSwitch) Update() error {
	req, err := s.getRequest("GetInsightParams", "insight")
	if err != nil {
		return err
	}
	client := http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var state wemoInsight
	xml.Unmarshal(body, &state)
	state.parse()
	s.Insight = state
	s.lastUpdated = time.Now().Unix()
	return nil
}

func (s *WemoSwitch) setBinaryState(signal string) error {
	binaryState := `<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:SetBinaryState xmlns:u="urn:Belkin:service:basicevent:1"><BinaryState>` + signal + `</BinaryState></u:SetBinaryState></s:Body></s:Envelope>`
	url := "http://" + s.Host + "/upnp/control/basicevent1"
	req, err := http.NewRequest("POST", url, strings.NewReader(binaryState))
	if err != nil {
		return err
	}
	req.Header.Add("SOAPACTION", `"urn:Belkin:service:basicevent:1#SetBinaryState"`)
	req.Header.Add("Content-type", `text/xml; charset="utf-8"`)
	client := http.Client{}
	_, err = client.Do(req)
	return err
}

func (s *WemoSwitch) getRequest(action, some string) (*http.Request, error) {
	serviceUrn := fmt.Sprintf("urn:Belkin:service:%s:1", some)

	body := fmt.Sprintf("<u:%[1]s xmlns:u=\"%[2]s\"></u:%[1]s>", action, serviceUrn)
	reqXml := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"utf-8\"?><s:Envelope xmlns:s=\"http://schemas.xmlsoap.org/soap/envelope/\" s:encodingStyle=\"http://schemas.xmlsoap.org/soap/encoding/\"><s:Body>%s</s:Body></s:Envelope>", body)

	url := fmt.Sprintf("http://%s/upnp/control/%s1", s.Host, some)
	req, err := http.NewRequest("POST", url, strings.NewReader(reqXml))
	if err != nil {
		return req, err
	}
	req.Header.Add("SOAPACTION", fmt.Sprintf("\"urn:Belkin:service:%s:1#%s\"", some, action))
	req.Header.Add("Content-type", `text/xml; charset="utf-8"`)
	return req, nil
}

type wemoSetupXML struct {
	XMLName      xml.Name `xml:"root"`
	FriendlyName string   `xml:"device>friendlyName"`
}

type wemoBinaryStateXML struct {
	XMLName     xml.Name `xml:"Envelope"`
	BinaryState int      `xml:"Body>GetBinaryStateResponse>BinaryState"`
}

func getWemoInfo(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		var empty []byte
		return empty, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	return body, err
}
