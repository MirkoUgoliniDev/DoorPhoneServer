package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/pprof"

	"github.com/MirkoUgoliniDev/doorphoneserver"
)

type contextKey string

const (
	ConfigPathKey contextKey = "configPath"
)

var (
	cpuprofile  = flag.String("cpuprofile", "", "write cpu profile `file`")
	memprofile  = flag.String("memprofile", "", "write memory profile to `file`")
	serverindex = flag.String("serverindex", "0", "jump to server index [n]")
	configPath  = "doorphoneserver.xml"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, ConfigPathKey, configPath)
	log.Println("-----> Contesto globale inizializzato con configPath:", configPath)

	doorphoneserver.SetGlobalContext(ctx, cancel)

	config := flag.String("config", configPath, "full path to doorphoneserver.xml configuration file")
	flag.Usage = doorphoneserverusage
	flag.Parse()

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal("could not create CPU profile: ", err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal("could not start CPU profile: ", err)
		}
		defer pprof.StopCPUProfile()
	}

	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			log.Fatal("could not create memory profile: ", err)
		}
		runtime.GC()
		if err := pprof.WriteHeapProfile(f); err != nil {
			log.Fatal("could not write memory profile: ", err)
		}
		f.Close()
	}

	doorphoneserver.Init(*config, *serverindex)
}

func doorphoneserverusage() {
	fmt.Println("---------------------------------------------------------------------------------------")
	fmt.Println("Usage: doorphoneserver [-config=[full path to doorphoneserver.xml]]")
	fmt.Println("By Mirko Ugolini <mirko.ugolini@gmail.com>")
	fmt.Println("---------------------------------------------------------------------------------------")
	fmt.Println("-config=/home/doorphoneserver/doorphoneserver.xml")
	fmt.Println("-serverindex=[n] for the index of the enabled server to connect to in XML file")
	fmt.Println("-help for this screen")
}
