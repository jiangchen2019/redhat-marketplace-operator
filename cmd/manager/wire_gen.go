// Code generated by Wire. DO NOT EDIT.

//go:generate wire
//+build !wireinject

package main

import (
	"github.ibm.com/symposium/redhat-marketplace-operator/pkg/controller"
	"github.ibm.com/symposium/redhat-marketplace-operator/pkg/managers"
)

// Injectors from wire.go:

func initializeMarketplaceController() *managers.ControllerMain {
	marketplaceController := controller.ProvideMarketplaceController()
	meterbaseController := controller.ProvideMeterbaseController()
	meterDefinitionController := controller.ProvideMeterDefinitionController()
	razeeDeployController := controller.ProvideRazeeDeployController()
	olmSubscriptionController := controller.ProvideOlmSubscriptionController()
	controllerFlagSet := controller.ProvideControllerFlagSet()
	opsSrcSchemeDefinition := managers.ProvideOpsSrcScheme()
	monitoringSchemeDefinition := managers.ProvideMonitoringScheme()
	olmV1SchemeDefinition := managers.ProvideOLMV1Scheme()
	olmV1Alpha1SchemeDefinition := managers.ProvideOLMV1Alpha1Scheme()
	controllerMain := makeMarketplaceController(marketplaceController, meterbaseController, meterDefinitionController, razeeDeployController, olmSubscriptionController, controllerFlagSet, opsSrcSchemeDefinition, monitoringSchemeDefinition, olmV1SchemeDefinition, olmV1Alpha1SchemeDefinition)
	return controllerMain
}
