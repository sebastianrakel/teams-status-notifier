package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/spf13/viper"
)

type Config struct {
	TenantID     string     `mapstructure:"tenant_id"`
	ClientID     string     `mapstructure:"client_id"`
	ClientSecret string     `mapstructure:"client_secret"`
	MQTT         MqttConfig `mapstructure:"mqtt"`
	Interval     int        `mapstructure:"interval"`
	GroupId      string     `mapstructure:"group_id"`
}

type MqttConfig struct {
	Broker   string `mapstructure:"broker"`
	Port     int    `mapstructure:"port"`
	Topic    string `mapstructure:"topic"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

type User struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	UserPrincipalName string `json:"userPrincipalName"`
}

type UsersResponse struct {
	Users []User `json:"value"`
}

type Presence struct {
	ID           string `json:"id"`
	Availability string `json:"availability"`
	Activity     string `json:"activity"`
}

type PresenceResponse struct {
	Presences []Presence `json:"value"`
}

type UserStatus struct {
	UserID       string `json:"user_id"`
	DisplayName  string `json:"display_name"`
	Email        string `json:"email"`
	Availability string `json:"availability"`
	Activity     string `json:"activity"`
	Timestamp    int64  `json:"timestamp"`
}

type Member struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	UserPrincipalName string `json:"userPrincipalName"`
	ODataType         string `json:"@odata.type"`
}

type MembersResponse struct {
	Members []Member `json:"value"`
}

var config Config
var accessToken string
var tokenExpiry time.Time

func init() {
	viper.SetEnvPrefix("TSN")
	viper.AutomaticEnv()

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("/etc/teams-status-notifier/")

	viper.SetDefault("mqtt.port", 1883)
	viper.SetDefault("mqtt.topic", "teams/status")
	viper.SetDefault("interval", 60)

	viper.BindEnv("mqtt.broker", "TSN_MQTT_BROKER")
	viper.BindEnv("mqtt.port", "TSN_MQTT_PORT")
	viper.BindEnv("mqtt.topic", "TSN_MQTT_TOPIC")
	viper.BindEnv("mqtt.username", "TSN_MQTT_USERNAME")
	viper.BindEnv("mqtt.password", "TSN_MQTT_PASSWORD")

	viper.BindEnv("tenant_id", "TSN_TENANT_ID")
	viper.BindEnv("client_id", "TSN_CLIENT_ID")
	viper.BindEnv("client_secret", "TSN_CLIENT_SECRET")
	viper.BindEnv("group_id", "TSN_GROUP_ID")

	viper.BindEnv("interval", "TSN_INTERVAL")

	err := viper.ReadInConfig()

	if err != nil {
		log.Printf("%v", err)
	}
	err = viper.Unmarshal(&config)
	if err != nil {
		log.Printf("unable to decode into config struct, %v", err)
	}
}

func getAccessToken() error {
	if time.Now().Before(tokenExpiry) && accessToken != "" {
		return nil
	}

	url := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", config.TenantID)

	data := fmt.Sprintf("client_id=%s&scope=https://graph.microsoft.com/.default&client_secret=%s&grant_type=client_credentials",
		config.ClientID, config.ClientSecret)

	resp, err := http.Post(url, "application/x-www-form-urlencoded", bytes.NewBufferString(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return err
	}

	accessToken = tokenResp.AccessToken
	tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn-300) * time.Second)

	return nil
}

func makeGraphRequest(url string) ([]byte, error) {
	if err := getAccessToken(); err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	result, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func getGroupMembers(groupID string) ([]Member, error) {
	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/groups/%s/members", groupID)
	body, err := makeGraphRequest(url)
	if err != nil {
		return nil, err
	}

	var membersResp MembersResponse
	if err := json.Unmarshal(body, &membersResp); err != nil {
		return nil, err
	}

	return membersResp.Members, nil
}

func getUsersFromGroup() ([]User, error) {
	members, err := getGroupMembers(config.GroupId)
	if err != nil {
		return nil, err
	}

	var users []User
	for _, member := range members {
		if member.ODataType == "#microsoft.graph.user" {
			users = append(users, User{
				ID:                member.ID,
				DisplayName:       member.DisplayName,
				UserPrincipalName: member.UserPrincipalName,
			})
		}
	}

	return users, nil
}

func getPresences(userIDs []string) ([]Presence, error) {
	requestBody := map[string][]string{
		"ids": userIDs,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}

	if err := getAccessToken(); err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", "https://graph.microsoft.com/v1.0/communications/getPresencesByUserId", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var presenceResp PresenceResponse
	if err := json.Unmarshal(body, &presenceResp); err != nil {
		return nil, err
	}

	return presenceResp.Presences, nil
}

func publishToMQTT(client mqtt.Client, statuses []UserStatus) {
	for _, status := range statuses {
		payload, err := json.Marshal(status)
		if err != nil {
			log.Printf("Error marshaling status: %v", err)
			continue
		}

		topic := fmt.Sprintf("%s/%s", config.MQTT.Topic, status.Email)
		token := client.Publish(topic, 0, false, payload)
		token.Wait()
	}
}

func main() {
	log.Printf("MQTT Broker: %s", config.MQTT.Broker)

	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", config.MQTT.Broker, config.MQTT.Port))
	opts.SetClientID("teams-status-notifier")

	opts.Username = config.MQTT.Username
	opts.Password = config.MQTT.Password

	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Fatal("MQTT connection failed:", token.Error())
	}
	defer client.Disconnect(250)

	ticker := time.NewTicker(time.Duration(config.Interval) * time.Second)
	defer ticker.Stop()

	for {
		users, err := getUsersFromGroup()
		if err != nil {
			log.Printf("Error getting users: %v", err)
			<-ticker.C
			continue
		}

		var userIDs []string
		userMap := make(map[string]User)
		for _, user := range users {
			userIDs = append(userIDs, user.ID)
			userMap[user.ID] = user
		}

		presences, err := getPresences(userIDs)
		if err != nil {
			log.Printf("Error getting presences: %v", err)
			<-ticker.C
			continue
		}

		var statuses []UserStatus
		for _, presence := range presences {
			if user, exists := userMap[presence.ID]; exists {
				status := UserStatus{
					UserID:       presence.ID,
					DisplayName:  user.DisplayName,
					Email:        user.UserPrincipalName,
					Availability: presence.Availability,
					Activity:     presence.Activity,
					Timestamp:    time.Now().Unix(),
				}
				statuses = append(statuses, status)
			}
		}

		publishToMQTT(client, statuses)
		log.Printf("Published status for %d users", len(statuses))

		<-ticker.C
	}
}
