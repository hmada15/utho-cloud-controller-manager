package controller

import (
	"context"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	appsv1alpha1 "github.com/uthoplatforms/utho-cloud-controller-manager/api/v1alpha1"
)

func (r *UthoApplicationReconciler) DeleteLB(ctx context.Context, app *appsv1alpha1.UthoApplication, l *logr.Logger) error {
	lbID := app.Status.LoadBalancerID

	if lbID == "" {
		return errors.New(LBIDNotFound)
	}

	l.Info("Deleting LB")
	_, err := (*uthoClient).Loadbalancers().Delete(lbID)
	if err != nil {
		return errors.Wrap(err, "Error Deleting LB")
	}

	l.Info("Updating Status Field")
	app.Status.Phase = appsv1alpha1.LBDeletedPhase
	if err = r.Status().Update(ctx, app); err != nil {
		return errors.Wrap(err, "Error Updating LB Status.")
	}
	return nil
}

func (r *UthoApplicationReconciler) DeleteTargetGroups(ctx context.Context, app *appsv1alpha1.UthoApplication, l *logr.Logger) error {

	l.Info("Deleting Target Groups")
	tgs := app.Status.TargetGroupsID

	for i, tg := range tgs {
		if err := DeleteTargetGroup(tg, app.Spec.TargetGroups[i].Name); err != nil {
			return err
		}
	}

	app.Status.Phase = appsv1alpha1.TGDeletedPhase
	if err := r.Status().Update(ctx, app); err != nil {
		return errors.Wrap(err, "Error Updating Target Groups Deletion Status.")
	}
	return nil
}

func DeleteTargetGroup(id, name string) error {
	_, err := (*uthoClient).TargetGroup().Delete(id, name)
	if err != nil {
		return errors.Wrapf(err, "Error Deleting Target Group with ID: %s znd Name: %s", id, name)
	}
	return nil
}
