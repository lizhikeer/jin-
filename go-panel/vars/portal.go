package vars

import "fmt"

var portal = `
   _ ___ _   _        _  ___ _ _  ____                                
  | |_ _| \ | |      | |/ (_) | |/ ___|___  _   _ _ __ ___  ___       
  | | | |  \| |_____ | ' /| | | | |   / _ \| | | | '__/ __|/ _ \      
  | | | | |\  |_____|| . \| | | | |__| (_) | |_| | |  \__ \  __/      
  |_|___|_| \_|      |_|\_\_|_|_|\____\___/ \__,_|_|  |___/\___|      

JIN-KillCourse[https://github.com/lizhikeer/jin-]      version: ` + Version + `
`

func ShowPortal() {
	fmt.Println(portal)
}
