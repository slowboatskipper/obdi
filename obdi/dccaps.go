// Obdi - a REST interface and GUI for deploying software
// Copyright (C) 2014  Mark Clarkson
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package main

// All api calls have the username and GUID to be sent as part of the request

import (
	"fmt"
	//"bytes"
	//"net/url"
	//"time"
	//"github.com/jinzhu/gorm"
	"github.com/mclarkson/obdi/external/ant0ine/go-json-rest/rest"
	"strconv"
)

func (api *Api) GetAllDcCaps(w rest.ResponseWriter, r *rest.Request) {

	// Check credentials

	login := r.PathParam("login")
	guid := r.PathParam("GUID")

	// Only admin is allowed

	if login != "admin" {
		rest.Error(w, "Not allowed", 400)
		return
	}

	//session := Session{}
	var errl error = nil
	//if session,errl = api.CheckLogin( login, guid ); errl != nil {
	if _, errl = api.CheckLogin(login, guid); errl != nil {
		rest.Error(w, errl.Error(), 401)
		return
	}

	defer api.TouchSession(guid)

	qs := r.URL.Query() // Query string - map[string][]string

	dccaps := []DcCap{}

	if len(qs["code"]) > 0 {
		srch := qs["code"][0]
		mutex.Lock()
		api.db.Order("code").Find(&dccaps, "code = ?", srch)
		mutex.Unlock()
	} else {
		mutex.Lock()
		err := api.db.Order("Code").Find(&dccaps)
		mutex.Unlock()
		if err.Error != nil {
			if !err.RecordNotFound() {
				rest.Error(w, err.Error.Error(), 500)
				return
			}
		}
	}

	// Create a slice of maps from users struct
	// to selectively copy database fields for display

	u := make([]map[string]interface{}, len(dccaps))
	for i := range dccaps {
		u[i] = make(map[string]interface{})
		u[i]["Id"] = dccaps[i].Id
		u[i]["Code"] = dccaps[i].Code
		u[i]["Desc"] = dccaps[i].Desc
	}

	// Too much noise
	//api.LogActivity( session.Id, "Sent list of users" )
	w.WriteJson(&u)
}

func (api *Api) AddDcCap(w rest.ResponseWriter, r *rest.Request) {

	// Check credentials

	login := r.PathParam("login")
	guid := r.PathParam("GUID")

	// Only admin is allowed

	if login != "admin" {
		rest.Error(w, "Not allowed", 400)
		return
	}

	session := Session{}
	var errl error
	if session, errl = api.CheckLogin(login, guid); errl != nil {
		rest.Error(w, errl.Error(), 401)
		return
	}

	defer api.TouchSession(guid)

	// Can't add if it exists already

	DcCapData := DcCap{}

	if err := r.DecodeJsonPayload(&DcCapData); err != nil {
		rest.Error(w, "Invalid data format received.", 400)
		return
	} else if len(DcCapData.Code) == 0 || len(DcCapData.Desc) == 0 {
		rest.Error(w, "A required field is empty.", 400)
		return
	}
	DcCap := DcCap{}
	mutex.Lock()
	if !api.db.Find(&DcCap, "code = ?", DcCapData.Code).RecordNotFound() {
		mutex.Unlock()
		rest.Error(w, "Record exists.", 400)
		return
	}
	mutex.Unlock()

	// Add DcCap

	mutex.Lock()
	if err := api.db.Save(&DcCapData).Error; err != nil {
		mutex.Unlock()
		rest.Error(w, err.Error(), 400)
		return
	}
	mutex.Unlock()

	text := fmt.Sprintf("Added new DcCap, '%s'.",
		DcCapData.Code)

	api.LogActivity(session.Id, text)
	w.WriteJson(DcCapData)
}

func (api *Api) UpdateDcCap(w rest.ResponseWriter, r *rest.Request) {

	// Check credentials

	login := r.PathParam("login")
	guid := r.PathParam("GUID")

	// Only admin is allowed

	if login != "admin" {
		rest.Error(w, "Not allowed", 400)
		return
	}

	session := Session{}
	var errl error
	if session, errl = api.CheckLogin(login, guid); errl != nil {
		rest.Error(w, errl.Error(), 401)
		return
	}

	defer api.TouchSession(guid)

	// Ensure user exists

	id := r.PathParam("id")

	// Check that the id string is a number
	if _, err := strconv.Atoi(id); err != nil {
		rest.Error(w, "Invalid id.", 400)
		return
	}

	// Load data from db, then ...
	DcCap := DcCap{}
	mutex.Lock()
	if api.db.Find(&DcCap, id).RecordNotFound() {
		mutex.Unlock()
		//rest.Error(w, err.Error(), 400)
		rest.Error(w, "Record not found.", 400)
		return
	}
	mutex.Unlock()

	// ... overwrite any sent fields
	if err := r.DecodeJsonPayload(&DcCap); err != nil {
		//rest.Error(w, err.Error(), 400)
		rest.Error(w, "Invalid data format received.", 400)
		return
	}

	// Force the use of the path id over an id in the payload
	Id, _ := strconv.Atoi(id)
	DcCap.Id = int64(Id)

	mutex.Lock()
	if err := api.db.Save(&DcCap).Error; err != nil {
		mutex.Unlock()
		rest.Error(w, err.Error(), 400)
		return
	}
	mutex.Unlock()

	text := fmt.Sprintf("Updated DcCap, '%s'.",
		DcCap.Code)

	api.LogActivity(session.Id, text)

	w.WriteJson("Success")
}

func (api *Api) DeleteDcCap(w rest.ResponseWriter, r *rest.Request) {

	// Check credentials

	login := r.PathParam("login")
	guid := r.PathParam("GUID")

	// Only admin is allowed

	if login != "admin" {
		rest.Error(w, "Not allowed", 400)
		return
	}

	session := Session{}
	var errl error
	if session, errl = api.CheckLogin(login, guid); errl != nil {
		rest.Error(w, errl.Error(), 401)
		return
	}

	defer api.TouchSession(guid)

	// Delete

	id := 0
	if id, errl = strconv.Atoi(r.PathParam("id")); errl != nil {
		rest.Error(w, "Invalid id.", 400)
		return
	}

	DcCap := DcCap{}
	mutex.Lock()
	if api.db.First(&DcCap, id).RecordNotFound() {
		mutex.Unlock()
		rest.Error(w, "Record not found.", 400)
		return
	}
	mutex.Unlock()

	mutex.Lock()
	if err := api.db.Delete(&DcCap).Error; err != nil {
		mutex.Unlock()
		rest.Error(w, err.Error(), 400)
		return
	}
	mutex.Unlock()

	text := fmt.Sprintf("Deleted DcCap, '%s'.",
		DcCap.Code)

	api.LogActivity(session.Id, text)

	w.WriteJson("Success")
}
