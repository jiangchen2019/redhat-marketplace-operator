// Code generated by Wire. DO NOT EDIT.

//go:generate wire
//+build !wireinject

package main

import (
	"github.ibm.com/symposium/redhat-marketplace-operator/cmd/managers"
	"github.ibm.com/symposium/redhat-marketplace-operator/pkg/controller"
)

import (
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

// Injectors from wire.go:

func initializeRazeeController() *managers.ControllerMain {
	razeeDeployController := controller.ProvideRazeeDeployController()
	controllerFlagSet := controller.ProvideControllerFlagSet()
	controllerMain := makeController(razeeDeployController, controllerFlagSet)
	return controllerMain
}
