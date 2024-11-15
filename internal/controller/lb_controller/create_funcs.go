package lb_controller

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	appsv1alpha1 "github.com/uthoplatforms/utho-cloud-controller-manager/api/v1alpha1"
	"github.com/uthoplatforms/utho-cloud-controller-manager/internal/controller"
	"github.com/uthoplatforms/utho-go/utho"
	"strconv"
	"strings"
)

// CreateUthoLoadBalancer creates a new Load Balancer using the Utho API and updates the status of the application
func (r *UthoApplicationReconciler) CreateUthoLoadBalancer(ctx context.Context, app *appsv1alpha1.UthoApplication, l *logr.Logger) error {
	kubernetesID, err := r.GetClusterID(ctx, l)
	if err != nil {
		return err
	}

	vpcId, err := GetVpcId(kubernetesID)
	if err != nil {
		return err
	}

	lbreq := utho.CreateLoadbalancerParams{
		Dcslug: app.Spec.LoadBalancer.Dcslug,
		Type:   app.Spec.LoadBalancer.Type,
		Vpc:    vpcId,
		Name:   app.Spec.LoadBalancer.Name,
	}
	newLB, err := (*uthoClient).Loadbalancers().Create(lbreq)
	if err != nil {
		return err
	}

	// Update the application status with the new Load Balancer ID and phase
	app.Status.LoadBalancerID = newLB.ID
	app.Status.Phase = appsv1alpha1.LBCreatedPhase

	lb, _ := GetLB(newLB.ID)
	app.Status.LoadBalancerIP = lb.IP

	fmt.Printf("%+v\n", newLB)
	l.Info("Updating LB Details in the Status")
	if err = r.Status().Update(ctx, app); err != nil {
		return errors.Wrap(err, "Error updating LB status")
	}

	return nil
}

// CreateTargetGroups creates all target groups defined in the application's specifications
func (r *UthoApplicationReconciler) CreateTargetGroups(ctx context.Context, app *appsv1alpha1.UthoApplication, l *logr.Logger) error {
	if strings.ToLower(app.Spec.LoadBalancer.Type) == "network" || strings.ToLower(app.Spec.LoadBalancer.Type) != "application" {
		return nil
	}
	for _, tg := range app.Spec.TargetGroups {
		err := r.CreateTargetGroup(ctx, &tg, app, l)
		if err != nil {
			if err.Error() == TGAlreadyExists {
				l.Info(TGAlreadyExists)
				continue
			}
			return errors.Wrap(err, "Unable to Create Target Group")
		}
	}
	app.Status.Phase = appsv1alpha1.TGCreatedPhase
	if err := r.Status().Update(ctx, app); err != nil {
		return err
	}
	return nil
}

// CreateTargetGroup creates a single target group using the Utho API and updates the status of application
func (r *UthoApplicationReconciler) CreateTargetGroup(ctx context.Context, tg *appsv1alpha1.TargetGroup, app *appsv1alpha1.UthoApplication, l *logr.Logger) error {
	l.Info("Creating Target Group")

	tgreq := utho.CreateTargetGroupParams{
		Name:                tg.Name,
		Protocol:            strings.ToUpper(tg.Protocol),
		HealthCheckPath:     tg.HealthCheckPath,
		HealthCheckProtocol: strings.ToUpper(tg.HealthCheckProtocol),
		Port:                fmt.Sprintf("%d", tg.Port),
		HealthCheckTimeout:  fmt.Sprintf("%d", tg.HealthCheckTimeout),
		HealthCheckInterval: fmt.Sprintf("%v", tg.HealthCheckInterval),
		HealthyThreshold:    fmt.Sprintf("%v", tg.HealthyThreshold),
		UnhealthyThreshold:  fmt.Sprintf("%v", tg.UnhealthyThreshold),
	}

	newTG, err := (*uthoClient).TargetGroup().Create(tgreq)
	if err != nil {
		//l.Error(err, "Unable to create TG")
		return err
	}
	// Add the new target group ID to the application's status
	l.Info("Adding TG ID to the Status Field")
	app.Status.TargetGroupsID = append(app.Status.TargetGroupsID, fmt.Sprintf("%d", newTG.ID))
	if err = r.Status().Update(ctx, app); err != nil {
		l.Error(err, "Unable to Add TG ID to State")
		return err
	}
	return nil
}

// CreateNLBBackend adds the Kubernetes Cluster as the Backend to the Load Balancer using the Utho API
func (r *UthoApplicationReconciler) CreateNLBBackend(ctx context.Context, app *appsv1alpha1.UthoApplication, l *logr.Logger) error {
	lbID := app.Status.LoadBalancerID
	if lbID == "" {
		return errors.New(LBIDNotFound)
	}
	frontendID := app.Status.FrontendID
	if frontendID == "" {
		return errors.New(FrontendIDNotFound)
	}

	kubernetesID, err := r.GetClusterID(ctx, l)
	if err != nil {
		return errors.Wrap(err, "Unable to Get Cluster ID")
	}
	l.Info("Creating Backend for NLB")
	params := utho.CreateLoadbalancerBackendParams{
		LoadbalancerId: lbID,
		Cloudid:        kubernetesID,
		BackendPort:    fmt.Sprintf("%d", app.Spec.LoadBalancer.BackendPort),
		FrontendID:     frontendID,
	}
	_, err = (*uthoClient).Loadbalancers().CreateBackend(params)
	if err != nil {
		return errors.Wrap(err, "Error Creating Backend for NLB")
	}

	// Update the application status phase to indicate the backend has been created
	app.Status.Phase = appsv1alpha1.ACLCreatedPhase
	if err := r.Status().Update(ctx, app); err != nil {
		return errors.Wrap(err, "Error Updating Backend Created Phase")
	}

	l.Info("Backend Created")
	return nil
}

// CreateLBFrontend creates a frontend for the Load Balancer using the Utho API and updates the status of the application
func (r *UthoApplicationReconciler) CreateLBFrontend(ctx context.Context, app *appsv1alpha1.UthoApplication, l *logr.Logger) error {

	lbID := app.Status.LoadBalancerID
	if lbID == "" {
		return errors.New(LBIDNotFound)
	}

	lb, err := GetLB(lbID)
	if err != nil {
		return errors.Wrap(err, "Error Getting LB")
	}

	// Create frontend if none exists
	if len(lb.Frontends) == 0 {
		frontend := app.Spec.LoadBalancer.Frontend

		params := &utho.CreateLoadbalancerFrontendParams{
			LoadbalancerId: lbID,
			Name:           frontend.Name,
			Proto:          strings.ToLower(frontend.Protocol),
			Port:           fmt.Sprintf("%d", frontend.Port),
			Algorithm:      strings.ToLower(frontend.Algorithm),
			Redirecthttps:  controller.TrueOrFalse(frontend.RedirectHttps),
			Cookie:         controller.TrueOrFalse(frontend.Cookie),
		}
		certificateID, err := GetCertificateID(frontend.CertificateName, l)
		if err != nil {
			if err.Error() == CertificateIDNotFound {
				l.Info("Certificate ID not found")
			} else {
				return errors.Wrap(err, "Error Getting Certificate ID")
			}
		}

		if certificateID != "" {
			params.CertificateID = certificateID
		}

		l.Info("Creating Frontend for LB")
		res, err := (*uthoClient).Loadbalancers().CreateFrontend(*params)
		if err != nil {
			return errors.Wrap(err, "Error Creating Frontend")
		}

		app.Status.FrontendID = res.ID
		app.Status.Phase = appsv1alpha1.FrontendCreatedPhase

		err = r.Status().Update(ctx, app)
		if err != nil {
			return errors.Wrap(err, "Error Updating Frontend in Status")
		}
	} else {
		// If frontend already exists, update the application status with the existing frontend
		app.Status.FrontendID = lb.Frontends[0].ID
		app.Status.Phase = appsv1alpha1.FrontendCreatedPhase

		err = r.Status().Update(ctx, app)
		if err != nil {
			return errors.Wrap(err, "Error Updating Frontend in Status")
		}
	}
	return nil
}

// CreateACLRules create ACL rules for the Load Balancer using Utho API and updates the status of the application
func (r *UthoApplicationReconciler) CreateACLRules(ctx context.Context, app *appsv1alpha1.UthoApplication, l *logr.Logger) error {

	if strings.ToLower(app.Spec.LoadBalancer.Type) == "network" || strings.ToLower(app.Spec.LoadBalancer.Type) != "application" {
		return r.CreateNLBBackend(ctx, app, l)
	}
	l.Info("Creating ACL Rules")
	rules := app.Spec.LoadBalancer.ACL
	for _, rule := range rules {
		if err := r.CreateACLRule(ctx, app, &rule, l); err != nil {
			if err.Error() == ACLAlreadyExists {
				l.Info("ACL Rule already exists")
				continue
			}
			return err
		}
	}
	// Update the application status phase to indicate ACL Rules have been created
	app.Status.Phase = appsv1alpha1.ACLCreatedPhase
	if err := r.Status().Update(ctx, app); err != nil {
		return errors.Wrap(err, "Error Updating ACL Created Phase")
	}

	l.Info("ACL Rules Created")
	return nil
}

// CreateACLRule creates a single ACL rule for the Load Balancer using Utho API
func (r *UthoApplicationReconciler) CreateACLRule(ctx context.Context, app *appsv1alpha1.UthoApplication, rule *appsv1alpha1.ACLRule, l *logr.Logger) error {
	frontendID := app.Status.FrontendID
	if frontendID == "" {
		return errors.New(FrontendIDNotFound)
	}

	lbID := app.Status.LoadBalancerID
	if lbID == "" {
		return errors.New(LBIDNotFound)
	}

	l.Info("Creating ACL Rule")
	rule.Value.FrontendID = frontendID
	byteValue, err := json.Marshal(rule.Value)
	if err != nil {
		return errors.Wrap(err, "Error Marshalling ACL Rule")
	}
	// Creating parameters to create ACL Rule
	params := utho.CreateLoadbalancerACLParams{
		LoadbalancerId: lbID,
		Name:           rule.Name,
		ConditionType:  rule.ConditionType,
		FrontendID:     frontendID,
		Value:          string(byteValue),
	}
	res, err := (*uthoClient).Loadbalancers().CreateACL(params)
	if err != nil {
		return err
	}

	// Updating ACL Rule ID to status of application
	app.Status.ACLRuleIDs = append(app.Status.ACLRuleIDs, res.ID)
	if err = r.Status().Update(ctx, app); err != nil {
		return errors.Wrap(err, "Error Updating ACL Rule ID in Status Field")
	}
	return nil
}

func (r *UthoApplicationReconciler) CreateAdvancedRoutingRules(ctx context.Context, app *appsv1alpha1.UthoApplication, l *logr.Logger) error {
	if strings.ToLower(app.Spec.LoadBalancer.Type) == "network" || strings.ToLower(app.Spec.LoadBalancer.Type) != "application" {
		return nil
	}
	l.Info("Creating Advanced Routing Rules")

	app.Status.Phase = appsv1alpha1.AdvancedRoutingPendingPhase
	if err := r.Status().Update(ctx, app); err != nil {
		return errors.Wrap(err, "Error Updating Advanced Routing Pending Phase")
	}
	rules := app.Spec.LoadBalancer.AdvancedRoutingRules
	for _, rule := range rules {
		if err := r.CreateAdvancedRoutingRule(ctx, app, &rule, l); err != nil {
			return err
		}
	}

	app.Status.Phase = appsv1alpha1.AdvancedRoutingCreatedPhase
	if err := r.Status().Update(ctx, app); err != nil {
		return errors.Wrap(err, "Error Updating Advanced Routing Created Phase")
	}
	return nil
}

func (r *UthoApplicationReconciler) CreateAdvancedRoutingRule(ctx context.Context, app *appsv1alpha1.UthoApplication, rule *appsv1alpha1.AdvancedRoutingRule, l *logr.Logger) error {
	lbID := app.Status.LoadBalancerID
	if lbID == "" {
		return errors.New(LBIDNotFound)
	}

	l.Info("Creating Advanced Routing Rule")

	params := &utho.CreateLoadbalancerRouteParams{
		LoadbalancerId: lbID,
		FrontendID:     app.Status.FrontendID,
		RouteCondition: strconv.FormatBool(rule.RouteCondition),
	}

	for i, acl := range app.Spec.LoadBalancer.ACL {
		if acl.Name == rule.ACLName {
			params.ACLID = app.Status.ACLRuleIDs[i]
		}
	}

	tgMap := make(map[string]string)
	for i, tg := range app.Spec.TargetGroups {
		tgMap[strings.ToLower(tg.Name)] = app.Status.TargetGroupsID[i]
	}

	var tgIDs string
	for _, tgName := range rule.TargetGroupNames {
		tgID, ok := tgMap[strings.ToLower(tgName)]
		if !ok {
			r.Recorder.Event(app, "Warning", "TargetGroupNotFound", fmt.Sprintf("Target Group %s not found in Spec", tgName))
		}
		tgIDs = tgIDs + "," + tgID
	}

	params.TargetGroups = strings.TrimPrefix(tgIDs, ",")
	l.Info("Advanced", "params", params)

	_, err := (*uthoClient).Loadbalancers().CreateRoute(*params)
	if err != nil {
		if err.Error() == LBAlreadyDeleted {
			l.Info("No LB Found")
			return nil
		}
		if err.Error() == RoutingRuleAlreadyExists {
			l.Info("Routing Rule Already Exists")
			//if !controller.ContainsString(app.Status.AdvancedRoutingRulesIDs, res.ID) {
			//	app.Status.AdvancedRoutingRulesIDs = append(app.Status.AdvancedRoutingRulesIDs, res.ID)
			//	if err = r.Status().Update(ctx, app); err != nil {
			//		return errors.Wrap(err, "Error Updating Advanced Routing Rule ID in Status Field")
			//	}
			//}
			return nil
		}
		return errors.Wrap(err, "Error Creating Routing Rule")
	}
	//app.Status.AdvancedRoutingRulesIDs = append(app.Status.AdvancedRoutingRulesIDs, res.ID)
	//if err = r.Status().Update(ctx, app); err != nil {
	//	return errors.Wrap(err, "Error Updating Advanced Routing Rule ID in Status Field")
	//}
	return nil
}
