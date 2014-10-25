// Deployment Manager - a REST interface and GUI for deploying software
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

import (
  "log"
  "log/syslog"
  "net"
  "net/rpc"
  "encoding/json"
  "fmt"
  "os"
  "sync"
  "strings"
  "regexp"
  "github.com/jinzhu/gorm"
  _ "github.com/mattn/go-sqlite3"
)

// ***************************************************************************
// SQLITE3 PRIVATE DB
// ***************************************************************************

type Enc struct {
  Id        int64
  SaltId    string    // Name of the server
  Formula   string    // Directory name
  StateFile string    // Sls file name
  Dc        string    // Data centre name
  Env       string    // Environment name
}

type Regex struct {
  Id        int64
  Regex     string
  Dc        string
  Env       string
}

type RegexSlsMap struct {
  Id        int64
  RegexId   int64     // Not null
  Formula   string    // Not null
  StateFile string    // Can be null
}

// --

var config *Config

type Config struct {
  Dbname    string
}

// --------------------------------------------------------------------------
func (c *Config) DBPath() string {
// --------------------------------------------------------------------------
  return c.Dbname
}

// --------------------------------------------------------------------------
func (c *Config) SetDBPath( path string ) {
// --------------------------------------------------------------------------
  c.Dbname = path
}

// --------------------------------------------------------------------------
func NewConfig() {
// --------------------------------------------------------------------------
  config = &Config{}
}

// --

type GormDB struct {
  db gorm.DB
}

// --------------------------------------------------------------------------
func (gormInst *GormDB) InitDB() error {
// --------------------------------------------------------------------------
  var err error
  dbname := config.DBPath()

  gormInst.db, err = gorm.Open("sqlite3", dbname + "enc.db")
  if err != nil {
    return ApiError{"Open " + dbname + " failed. " + err.Error()}
  }

  if err := gormInst.db.AutoMigrate(Enc{}).Error; err != nil {
    txt := fmt.Sprintf("AutoMigrate Enc table failed: %s", err)
    return ApiError{ txt }
  }
  if err := gormInst.db.AutoMigrate(Regex{}).Error; err != nil {
    txt := fmt.Sprintf("AutoMigrate Regex table failed: %s", err)
    return ApiError{ txt }
  }
  if err := gormInst.db.AutoMigrate(RegexSlsMap{}).Error; err != nil {
    txt := fmt.Sprintf("AutoMigrate RegexSlsMap table failed: %s", err)
    return ApiError{ txt }
  }

  // Unique index is also a constraint, so are forced to be unique
  gormInst.db.Model(Enc{}).AddIndex("idx_enc_salt_id", "salt_id")

  return nil
}

// --------------------------------------------------------------------------
func (gormInst *GormDB) DB() *gorm.DB {
// --------------------------------------------------------------------------
  return &gormInst.db
}

// --------------------------------------------------------------------------
func NewDB() (*GormDB,error) {
// --------------------------------------------------------------------------
  gormInst := &GormDB{}
  if err := gormInst.InitDB(); err != nil {
    return gormInst, err
  }
  return gormInst,nil
}

// ***************************************************************************
// ERRORS
// ***************************************************************************

const (
    SUCCESS = 0
    ERROR = 1
)

type ApiError struct {
  details string
}

// --------------------------------------------------------------------------
func (e ApiError) Error() string {
// --------------------------------------------------------------------------
  return fmt.Sprintf("%s", e.details)
}

// ***************************************************************************
// LOGGING
// ***************************************************************************

// --------------------------------------------------------------------------
func logit(msg string) {
// --------------------------------------------------------------------------
// Log to syslog
    log.Println(msg)
    l, err := syslog.New(syslog.LOG_ERR, "obdi")
    defer l.Close()
    if err != nil {
        log.Fatal("error writing syslog!")
    }

    l.Err(msg)
}

// ***************************************************************************
// GO RPC PLUGIN
// ***************************************************************************

// Args are send over RPC from the Manager
type Args struct {
  PathParams    map[string]string
  QueryString   map[string][]string
  PostData      []byte
  QueryType     string
}

type PostedData struct {
  Classes         []string
  Dc              string
  Environment     string
}

type Plugin struct{}

// The reply will be sent and output by the master
type Reply struct {
  // Add more if required
  EncData         string
  // Must have the following
  PluginReturn    int64        // 0 - success, 1 - error
  PluginError     string
}

// Global mutex
var dbmutex = &sync.Mutex{}

// --------------------------------------------------------------------------
func ReturnError(text string, response *[]byte) {
// --------------------------------------------------------------------------
    errtext := Reply{ "", ERROR, text }
    logit( text )
    jsondata, _ := json.Marshal( errtext )
    *response = jsondata
}

// --------------------------------------------------------------------------
func (t *Plugin) GetRequest(args *Args, response *[]byte) error {
// --------------------------------------------------------------------------

  // Check for required query string entries

  var err error

  //dc_sent := 0
  dc_name := ""     // E.g. OFFICE
  //env_sent := 0
  env_name := ""    // E.g. test_1.122
  env_version := ""    // E.g. test
  salt_id := ""

  if len(args.QueryString["dc"]) > 0 {
    dc_name = args.QueryString["dc"][0]
    //dc_sent = 1
  } else { // Added due to NOTE 1 above
    ReturnError( "The dc must be set", response )
    return nil
  }

  if len(args.QueryString["version"]) > 0 {
    env_version = args.QueryString["version"][0]
    //env_sent = 1
  } else { // Added due to NOTE 1 above
    ReturnError( "The version must be set", response )
    return nil
  }

  if len(args.QueryString["env"]) > 0 {
    env_name = args.QueryString["env"][0]
    //env_sent = 1
  } else { // Added due to NOTE 1 above
    ReturnError( "The env must be set", response )
    return nil
  }

  if len(args.QueryString["salt_id"]) == 0 {
    ReturnError( "'salt_id' must be set", response )
    return nil
  }

  salt_id = args.QueryString["salt_id"][0]

  // PluginDatabasePath is required to open our private db
  if len(args.PathParams["PluginDatabasePath"]) == 0 {
    ReturnError( "Internal Error: 'PluginDatabasePath' must be set",response )
    return nil
  }

  config.SetDBPath( args.PathParams["PluginDatabasePath"] )

  // Open/Create database
  var gormInst *GormDB
  if gormInst,err = NewDB(); err!=nil {
    txt := "GormDB open error for '" + config.DBPath() + "enc.db'. " +
           err.Error()
    ReturnError( txt, response )
    return nil
  }

  // Get ENC formula's and state files from enc tables
  // Do we care who can get this information? I'm guessing 'no'.

  db := gormInst.DB() // shortcut
  encs := []Enc{}

  // Search the encs DB

  dbmutex.Lock()
  if err := db.Find(&encs, "salt_id = ? and dc = ? and env = ?",
  salt_id, dc_name, env_name); err.Error != nil {
      if !err.RecordNotFound() {
        dbmutex.Unlock()
        ReturnError( err.Error.Error(), response )
        return nil
      }
  }
  dbmutex.Unlock()

  var encClasses      []string
  var encEnvironment  string

  customised := false

  if len(encs) == 0 {

    // ENC entry does not exist. Make one on the fly from regexes
    // and return it to the user. ENC entries are only created
    // to override regexes.

    // Get all regexes for this dc and env
    regexes := []Regex{}
    dbmutex.Lock()
    if err := db.Find(&regexes, "dc = ? and env = ?",
    dc_name, env_name); err.Error != nil {
        if !err.RecordNotFound() {
          dbmutex.Unlock()
          ReturnError( err.Error.Error(), response )
          return nil
        }
    }
    dbmutex.Unlock()

    // Apply regexes to salt id - see which regexes qualify

    matched := false

    for i := range regexes {
      tryRegex,err := regexp.Compile( regexes[i].Regex )
      if err == nil {
        if tryRegex.MatchString( salt_id ) {
          // Add classes to the EncData ??
          dbmutex.Lock()
          regexSlsMaps := []RegexSlsMap{}
          if err := db.Find(&regexSlsMaps,
          "regex_id = ?",regexes[i].Id); err.Error != nil {
              if !err.RecordNotFound() {
                dbmutex.Unlock()
                ReturnError( err.Error.Error(), response )
                return nil
              }
          }
          dbmutex.Unlock()
          for j := range regexSlsMaps {
              matched = true

              stripped := strings.TrimSuffix(regexSlsMaps[j].StateFile, ".sls")
              if len(stripped) > 0 {
                encClasses = append(encClasses, regexSlsMaps[j].Formula +
                                    "." + stripped)
              } else {
                encClasses = append(encClasses, regexSlsMaps[j].Formula)
              }
          }
        }
      } else {
        logit( fmt.Sprintf("Regex error with '%s' (%s,%s,%s): %s.",
        regexes[i].Regex,dc_name,env_name,env_name + " " + env_version,err) )
      }
    }

    // No regex either. Return empty handed

    if matched == false {

      logit("No classes found for " + salt_id +
      ", and could not find a regex!")

    } else {
      encEnvironment = env_name// + "_" + env_version
    }

  } else {

    // ENC entry exists

    customised = true

    for i := range encs {

      stripped := strings.TrimSuffix(encs[i].StateFile, ".sls")

      if len(stripped) > 0 {
        encClasses = append(encClasses, encs[i].Formula + "." + stripped)
      } else {
        encClasses = append(encClasses, encs[i].Formula)
      }
    }
    encEnvironment = env_name// + "_" + env_version
  }

  // Output as JSON or YAML

  var EncData string

  // Differences between data included in yaml and json output.
  //
  // yaml: Sends "environment_version", so salt can match the git tag/branch.
  //
  // json: Send environment name only.
  //       Includes 'customised' field. So GUI can tell where the data was
  //       sourced from - enc or regex.

  if len(args.QueryString["yaml"]) > 0 {

    // Output as YAML

    if len(encClasses) == 0 {
      EncData = "classes: null"
    } else {
      EncData = "classes:\n"
      for i := range encClasses {
        EncData += "  - " + encClasses[i] + "\n"
      }
      EncData += "environment: " + encEnvironment + "_" + env_version
    }
  } else {

    // Output as JSON

    type JsonOut struct {
      Classes     []string
      Environment string
      Customised  bool
    }

    jsonout := JsonOut { encClasses, encEnvironment, customised }

    TempEncData, err := json.Marshal(jsonout)

    if err != nil {
      ReturnError( "Marshal error: "+err.Error(), response )
      return nil
    }

    EncData = string( TempEncData )
  }

  // Reply with the EncData (back to the master)

  reply := Reply{ EncData,SUCCESS,"" }
  jsondata, err := json.Marshal(reply)

  if err != nil {
    ReturnError( "Marshal error: "+err.Error(), response )
    return nil
  }

  *response = jsondata

  return nil
}

// --------------------------------------------------------------------------
func (t *Plugin) PostRequest(args *Args, response *[]byte) error {
// --------------------------------------------------------------------------

  //ReturnError( "Internal error: Unimplemented HTTP POST with data " +
  //  fmt.Sprintf(": %s",args.PostData), response )
  //return nil

  var err error

  if len(args.QueryString["salt_id"]) == 0 {
    ReturnError( "'salt_id' must be set", response )
    return nil
  }

  salt_id := args.QueryString["salt_id"][0]

  // Needed if the salt version has been changed
  if len(args.QueryString["env_id"]) == 0 {
    ReturnError( "'env_id' must be set", response )
    return nil
  }

  if err != nil {
    ReturnError( "Invalid env_id ('"+args.QueryString["env_id"][0]+"')",
    response )
    return nil
  }

  // PluginDatabasePath is required to open our private db
  if len(args.PathParams["PluginDatabasePath"]) == 0 {
    ReturnError( "Internal Error: 'PluginDatabasePath' must be set",response )
    return nil
  }

  config.SetDBPath( args.PathParams["PluginDatabasePath"] )

  // Open/Create database
  var gormInst *GormDB
  if gormInst,err = NewDB(); err!=nil {
    txt := "GormDB open error for '" + config.DBPath() + "enc.db'. " +
           err.Error()
    ReturnError( txt, response )
    return nil
  }

  // Decode the post data into struct

  var postedData PostedData

  if err := json.Unmarshal(args.PostData,&postedData); err != nil {
    txt := fmt.Sprintf("Error decoding JSON ('%s')"+ ".", err.Error())
    ReturnError( "Error decoding the POST data (" +
      fmt.Sprintf("%s",args.PostData) + "). " + txt, response )
    return nil
  }

  // Remove all ENC Classes (before adding)

  db := gormInst.DB() // shortcut

  dbmutex.Lock()
  if err := db.Where("salt_id = ? and dc = ? and env = ?",
  salt_id,postedData.Dc,postedData.Environment).Delete(Enc{});
  err.Error != nil {
      if !err.RecordNotFound() {
        dbmutex.Unlock()
        ReturnError( err.Error.Error(), response )
        return nil
      }
  }
  dbmutex.Unlock()

  // Add the ENC classes

  dbmutex.Lock()
  for i := range postedData.Classes {
    classes := strings.Split(postedData.Classes[i],".")
    formula := ""
    statefile := ""
    switch len(classes) {
      case 0: continue
      case 1: formula = classes[0]
      case 2: formula = classes[0]
              statefile = classes[1]
    }
    enc := Enc{
      SaltId:         salt_id,
      Formula:        formula,
      StateFile:      statefile,
      Dc:             postedData.Dc,
      Env:            postedData.Environment,
    }
    if err := db.Create(&enc); err.Error != nil {
      dbmutex.Unlock()
      ReturnError( err.Error.Error(), response )
      return nil
    }
  }
  dbmutex.Unlock()

  reply := Reply{ "",SUCCESS,"" }
  jsondata, err := json.Marshal(reply)

  if err != nil {
    ReturnError( "Marshal error: "+err.Error(), response )
    return nil
  }

  *response = jsondata

  return nil

}

// --------------------------------------------------------------------------
func (t *Plugin) HandleRequest(args *Args, response *[]byte) error {
// --------------------------------------------------------------------------
// All plugins must have this.

  if len(args.QueryType) > 0 {
    switch args.QueryType {
      case "GET": t.GetRequest(args, response)
                  return nil
      case "POST": t.PostRequest(args, response)
                   return nil
    }
    ReturnError( "Internal error: Invalid HTTP request type for this plugin " +
      args.QueryType, response )
    return nil
  } else {
    ReturnError( "Internal error: HTTP request type was not set", response )
    return nil
  }
}

// ***************************************************************************
// ENTRY POINT
// ***************************************************************************

// --------------------------------------------------------------------------
func main() {
// --------------------------------------------------------------------------

  // Sets the global config var
  NewConfig()

  plugin := new(Plugin)
  rpc.Register(plugin)

  listener, err := net.Listen("tcp", ":" + os.Args[1])
  if err != nil {
      txt := fmt.Sprintf( "Listen error. ", err )
      logit( txt )
  }

  //for {
    if conn, err := listener.Accept(); err != nil {
      txt := fmt.Sprintf( "Accept error. ", err )
      logit( txt )
    } else {
      rpc.ServeConn(conn)
    }
  //}
}

// vim:ts=2:sw=2:et