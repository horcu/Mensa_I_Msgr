package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/db"
	"firebase.google.com/go/v4/messaging"
	"fmt"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	models "github.com/horcu/peez_me_models"
	"github.com/tidwall/gjson"

	"google.golang.org/api/option"
	"html/template"
	"log"
)

// Variables used to generate the HTML page.
var (
	data       models.TemplateData
	tmpl       *template.Template
	client     *messaging.Client
	app        *firebase.App
	database   *db.Client
	ticketsRef *db.Ref
	userRef    *db.Ref
	invitesRef *db.Ref
)

var ctx = context.Background()

// Simple init to have the client, dbRefs, etc... available
func setup() {
	ctx = context.Background()

	//global variables here like firebase client etc...
	conf := &firebase.Config{
		ProjectID:   "peezme",
		DatabaseURL: "https://peezme-default-rtdb.firebaseio.com/",
	}

	decodedKey, err := getDecodedFireBaseKey()
	if err != nil {
		_ = fmt.Errorf("could not fetch credentials: %v", err)
	}

	opt := []option.ClientOption{option.WithCredentialsJSON(decodedKey)}

	// new firebase app instance
	app, err = firebase.NewApp(ctx, conf, opt...)

	if err != nil {
		_ = fmt.Errorf("error initializing firebase database app: %v", err)
	}

	client, err = app.Messaging(context.Background())
	if err != nil {
		_ = fmt.Errorf("error initializing firebase database app: %v", err)
	}

	database, err = app.Database(ctx)
	client, err = app.Messaging(ctx)

	if err != nil {
		_ = fmt.Errorf("error connecting to the database app: %v", err)
	}

	// Get a database reference to our game details.
	ticketsRef = database.NewRef("requests/")
	userRef = database.NewRef("users/")
	invitesRef = database.NewRef("invitations/")
}

//func main() {
//
//	//init the needed services so you do not rely on cold start init call
//	setup()
//
//	// Initialize template parameters.
//	service := os.Getenv("K_SERVICE")
//	if service == "" {
//		service = "???"
//	}
//
//	revision := os.Getenv("K_REVISION")
//	if revision == "" {
//		revision = "???"
//	}
//
//	// Prepare template for execution.
//	tmpl = template.Must(template.ParseFiles("index.html"))
//	data = models.TemplateData{
//		Service:  service,
//		Revision: revision,
//	}
//
//	// Define HTTP server.
//	mux := http.NewServeMux()
//	mux.HandleFunc("/invite", invitationHandler)
//
//	fs := http.FileServer(http.Dir("./assets"))
//	http.Handle("/assets/", http.StripPrefix("/assets/", fs))
//
//	// PORT environment variable is provided by Cloud Run.
//	port := os.Getenv("PORT")
//	if port == "" {
//		port = "8080"
//	}
//
//	log.Print("Hello from Cloud Run! The container started successfully and is listening for HTTP requests on $PORT")
//	log.Printf("Listening on port %s", port)
//	err := http.ListenAndServe(":"+port, mux)
//	if err != nil {
//		log.Fatal(err)
//	}
//}

func main() {
	// setup
	setup()

	// The default client is HTTP.
	c, err := cloudevents.NewClientHTTP()
	if err != nil {
		log.Fatalf("failed to create client, %v", err)
	}
	log.Fatal(c.StartReceiver(context.Background(), receive))
}
func receive(event cloudevents.Event) {

	var pmEvent *struct {
		Type  string      `json:"@type,omitempty"`
		Data  interface{} `json:"data,omitempty"`
		Delta interface{} `json:"delta,omitempty"`
	}

	err := event.DataAs(&pmEvent)
	if err != nil {
		fmt.Println("error converting event: \r\n" + err.Error())
	}

	fmt.Println("type")
	fmt.Println(pmEvent.Type)

	fmt.Println("delta:")
	fmt.Println(pmEvent.Delta)

	marshal, err := json.Marshal(pmEvent.Delta)
	if err != nil {
		return
	}

	var users []models.User
	gjson.Parse(string(marshal)).Get("invitees").ForEach(func(key, value gjson.Result) bool {

		fmt.Println("values:")
		fmt.Println(value.String())

		if key.String() == "0" {
			u, errInner := json.Marshal(value.Value())
			if errInner == nil {
				var usr models.User
				errInner2 := json.Unmarshal(u, &usr)
				if errInner2 == nil {
					users = append(users, usr)
				}
			}
		}
		return true
	})

	tick := models.Ticket{}
	json.Unmarshal(marshal, &tick)

	tick.Invitees = users

	_, inviteErr := Send(tick)
	if inviteErr != nil {
		fmt.Println("error sending invitations: \r\n" + inviteErr.Error())
		return
	}
	if err1 := markInvitationForDelete(tick); err1 != nil {
		fmt.Println("error marking invitations for delete: \r\n" + err.Error())
		return
	}

	//update existing ticket
	if err1, _ := updateTicket(tick); err1 != nil {
		fmt.Println("error updating tickets: \r\n" + err.Error())
		return
	}

}

func updateTicket(ticket models.Ticket) (error, bool) {
	ticketsRef = database.NewRef("requests/")
	ticket.InvitationSent = true

	err := ticketsRef.Child(ticket.CreatedBy).Child(ticket.Id).Set(ctx, &ticket)
	if err != nil {
		return err, false
	}
	return nil, true
}

// set flag that other services use to determine what to do with the tickets next
func markInvitationForDelete(t models.Ticket) error {

	// mark the invitation records for delete

	t.IsBeingProcessed = false
	t.IsActive = false
	t.InvitationSent = true
	for _, user := range t.Invitees {
		if t.AcceptedBy == nil {
			t.AcceptedBy = []models.User{}
		}

		invitesRef = database.NewRef("invitations/")

		err := invitesRef.Child(user.ID).Child(t.Id).Set(ctx, &t)
		if err != nil {
			_, err = fmt.Println("error: " + err.Error())
			return err
		}
	}

	return nil
}

func getDecodedFireBaseKey() ([]byte, error) {

	fireBaseAuthKey := `ewogICJxdW90YV9wcm9qZWN0IjogInBlZXptZSIsCiAgInR5cGUiOiAic2Vydmlj
ZV9hY2NvdW50IiwKICAicHJvamVjdF9pZCI6ICJwZWV6bWUiLAogICJwcml2YXRl
X2tleV9pZCI6ICJiNTZlZGJhMTRjMjlmMTVhOTEwZWM5MDc3YzlhMjRmNDI3YTJl
N2Q0IiwKICAicHJpdmF0ZV9rZXkiOiAiLS0tLS1CRUdJTiBQUklWQVRFIEtFWS0t
LS0tXG5NSUlFdlFJQkFEQU5CZ2txaGtpRzl3MEJBUUVGQUFTQ0JLY3dnZ1NqQWdF
QUFvSUJBUURWeVE0VWFBY3ppc0h5XG5ydnhVQlhsaFRTSWtmYXBxb25WelF4RVBa
S2RTdERwbWlUU1g1U25sS1g4RlU2c29zcXJ4anc2V1V1RWRIaHBrXG5OYzZLUjdE
MENCUlRzRXJYeGl3L3lJR1A4ZWtkbU0rK3A0TjV1cjNPVS9XbHhZeGhaMWpEdHcx
Z0F1VFJ2Zi81XG56T0JTbHh1UWVLUE5FK0krQjFNZVY4UXBPU3RYYmdDaHpkOWVw
NERsSERpd3gxL1A4R3ZPTGw5NlVzZFlmUUpjXG5ET3VZbnloVVpwZzc3R3praHhM
VEs5NU1pbjJZUjFlYTJJVnhXRDlja1pSVTNDQll4VmYyeDVMQU04aU1XdU9CXG56
dm5Pb2Z4cG9IZG9kWXZseVY2UU1iS3RQcnVpVUgrMkFveFNVQ1hRcUdxZ3BkcE52
a1J5TU1VOHdoSEFNRFc2XG4yWHQxUnoweEFnTUJBQUVDZ2dFQUNuMS9vZDVnUUJZ
THpLSHIyQjd6K2s0anpKWkNyRkhheTk3d3h0M0JzaUZpXG4zanVCcDZuRE9RU2t3
Q09xRHo0RDBscEZOU040VUFxZjhUdmlBeVMxR1B4ajF2UWNjQTNBTW04Snl5dVdn
ZkVlXG5KelQ4RXNIb09oTE04bnllUUtkUlorVWpGR0RWVG85eFcwRERpYzlJREhw
RysxNEJrNFB6ZndFcWdIcXNMRWJaXG5iWW9tMjN5V0RpR2ZmL2FzcVdYcFlMRlZP
QVdsNUNVQlEycW9Wd1FFYUh6TVU4a3V2LzhOcy9kd1p1NXZwNWJ1XG51bXB2KzVY
NlBHK202VDRqTVE3RnhyNTJBWGlTSkVUUzFlNENiMStyMHBYWk1wUDd1QkswNXZk
YUZPVU95Smp2XG5VT1J3UHk4Snp6WHlSdGdnQkJaTURiaXJsYnF4SUhuQXRwWnpr
OUYzR1FLQmdRRHVhUzU0V25RdGF4cllFa1FpXG5mbVJTYW9OdEMwdlpNYlo1V2dM
dGRZbkN5NzBWV0NLTkdRQzJHRitRY3dUUWwyT0VlVFVaZXRiTTFHeVFzZGNJXG55
dFc5dnJYWlJMQllSeVJqS3dLaEdQZEVHdytFUjRnSVZITy9tK1lYWGNlYllDUVRy
RVU4bDJZUkttWFh3OWUvXG44Umt2STNtTGVoa1VjdjhZYTBSeXZodWZWUUtCZ1FE
bGpzYlVNczBVKzFNWWpqVEFUZnhWeWU1OGo5WGNLZW83XG5pMUFVS1M5UWdSSGp3
Qi9PWDZKb1V2UXZGNTFUUWtDOVZOYWtvSm05em1mb3BUdFNnQ0NML3FlQ2xoRzV1
NmdDXG41a0xDRTk5TUpEM3dGVGw4MHNBRU5uZTBaS3RWVzAvSmpKZDVaT3JGYUlT
Mnp0NWFqNFBJalcxeU9PN0orQUoyXG5xd3k4T0JiT2JRS0JnRXZOQzJaZXRCT0F1
ejg1eDRvRUQ1ZVlvQUs2bGJvUHNVbXlFYjQ0SWIzYWsxckc4KzFTXG5wc1EreVp1
ZXhrZ2Y2aGREaGx0OGovRCtGU3FJTUt0dCtqbGkrbVNERDJKeDlDTEhtUVZwYjZ5
cXdlczM1d3RtXG45b3BVWWZySjZWNEFXbGdhN01TUUNuYW91VXE1ek00Tk5RbWt5
TTlNMmM2RHBaRzVBVUZPS25BbEFvR0FZQmNRXG40WGhXWWtjRnRJeXFSaWtlekNa
WDN1b3lnaE5GaWlFNXB3YktXRzkrdHBBUWdFbUY2UmQ0UVZJb045Yk8xTEh6XG5t
enZpdnhIc2F2VG5UUlIzQzBMUWlaZ1oyVjVVNk1uTC9nTmxnRERYZ0d6U0FJOFRj
Mi85VVpTbUozZXVnVmFKXG5mWFloMC9wNU96Q0M0UE9jSFZJZUV5Y0R4YVU4R3NK
azlWQ2hNMDBDZ1lFQWxkYy80bEd6UzNoenZLRytFc3hCXG5ISmMydzlsL3NqYVdW
MU5VVnk4dXhXUjRZZmFienFKMmxRcUhxbW9ub3FTbG5FenZqTSs1WG1aQzFPZlV3
dlhLXG5tK1VSenV5YXpBVlNHaXRMSWgzV3pZQ1p1YjhuNDhvU09maGcyT1AyWUNt
aDlKZnB0YURCa1hQVDZLcmY5b3ROXG5QR05kbkZpWk93aW9LY21SMGI1ZHpkaz1c
bi0tLS0tRU5EIFBSSVZBVEUgS0VZLS0tLS1cbiIsCiAgImNsaWVudF9lbWFpbCI6
ICJmaXJlYmFzZS1hZG1pbnNkay1iOGFpeEBwZWV6bWUuaWFtLmdzZXJ2aWNlYWNj
b3VudC5jb20iLAogICJjbGllbnRfaWQiOiAiMTA1MjM4Mjk2OTAxNzUzMDQzMjA2
IiwKICAiYXV0aF91cmkiOiAiaHR0cHM6Ly9hY2NvdW50cy5nb29nbGUuY29tL28v
b2F1dGgyL2F1dGgiLAogICJ0b2tlbl91cmkiOiAiaHR0cHM6Ly9vYXV0aDIuZ29v
Z2xlYXBpcy5jb20vdG9rZW4iLAogICJhdXRoX3Byb3ZpZGVyX3g1MDlfY2VydF91
cmwiOiAiaHR0cHM6Ly93d3cuZ29vZ2xlYXBpcy5jb20vb2F1dGgyL3YxL2NlcnRz
IiwKICAiY2xpZW50X3g1MDlfY2VydF91cmwiOiAiaHR0cHM6Ly93d3cuZ29vZ2xl
YXBpcy5jb20vcm9ib3QvdjEvbWV0YWRhdGEveDUwOS9maXJlYmFzZS1hZG1pbnNk
ay1iOGFpeCU0MHBlZXptZS5pYW0uZ3NlcnZpY2VhY2NvdW50LmNvbSIKIH0=`

	//fireBaseAuthKey = os.Getenv("APP_CREDS")
	decodedKey, err := base64.StdEncoding.DecodeString(fireBaseAuthKey)
	if err != nil {
		return nil, err
	}

	return decodedKey, nil
}

func Send(ticket models.Ticket) (bool, error) {

	var deviceTokens []string
	for _, us := range ticket.Invitees {
		deviceTokens = append(deviceTokens, us.FcmToken)
	}

	if len(deviceTokens) <= 0 {
		return false, errors.New("no users listed. Invitations not sent")
	}

	for _, token := range deviceTokens {
		message := &messaging.Message{
			Data: map[string]string{
				"id": ticket.Id,
			},
			Notification: &messaging.Notification{
				Title: "PeezMe",
				Body:  "Your friends would like to play!",
				//ImageURL: t.CreatedBy
			},
			Token: token,
		}

		_, err2 := client.Send(ctx, message)
		if err2 != nil {
			return false, err2
		}
		_, err2 = fmt.Println("message sent to : " + token)
		return true, nil
	}

	return false, errors.New("no device ids listed. Cannot send invitations")
}
