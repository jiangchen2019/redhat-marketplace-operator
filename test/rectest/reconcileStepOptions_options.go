package rectest

// Code generated by github.com/launchdarkly/go-options.  DO NOT EDIT.

type ApplyReconcileStepOptionFunc func(c *reconcileStepOptions) error

func (f ApplyReconcileStepOptionFunc) apply(c *reconcileStepOptions) error {
	return f(c)
}

func newReconcileStepOptions(options ...ReconcileStepOption) (reconcileStepOptions, error) {
	var c reconcileStepOptions
	err := applyReconcileStepOptionsOptions(&c, options...)
	return c, err
}

func applyReconcileStepOptionsOptions(c *reconcileStepOptions, options ...ReconcileStepOption) error {
	for _, o := range options {
		if err := o.apply(c); err != nil {
			return err
		}
	}
	return nil
}

type ReconcileStepOption interface {
	apply(*reconcileStepOptions) error
}

func ReconcileWithExpectedResults(o ...ReconcileResult) ApplyReconcileStepOptionFunc {
	return func(c *reconcileStepOptions) error {
		c.ExpectedResults = o
		return nil
	}
}

func ReconcileWithUntilDone(o bool) ApplyReconcileStepOptionFunc {
	return func(c *reconcileStepOptions) error {
		c.UntilDone = o
		return nil
	}
}

func ReconcileWithMax(o int) ApplyReconcileStepOptionFunc {
	return func(c *reconcileStepOptions) error {
		c.Max = o
		return nil
	}
}