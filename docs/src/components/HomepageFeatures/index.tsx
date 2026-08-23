import type {ReactNode} from 'react';
import clsx from 'clsx';
import Link from '@docusaurus/Link';
import Heading from '@theme/Heading';
import styles from './styles.module.css';

type FeatureItem = {
  title: string;
  kicker: string;
  description: ReactNode;
  to: string;
};

const FeatureList: FeatureItem[] = [
  {
    title: 'Pull, not push',
    kicker: 'claimNext(stationId, capabilities)',
    to: '/docs/business-context/why-pull-not-push',
    description: (
      <>
        A station asks for its next task and the system selects the work — never
        the other way round. There is deliberately no{' '}
        <code>assign(task, station)</code> anywhere in the model, so no
        allocation can go stale the moment the floor changes.
      </>
    ),
  },
  {
    title: 'Leased claims',
    kicker: 'at-most-once, never lost',
    to: '/docs/adr/0003-lease-based-at-most-once-claiming',
    description: (
      <>
        Every claim is time-boxed. Two stations can never hold the same task,
        and a claim that is not renewed or completed before its lease expires
        returns to the pool rather than stranding work nobody is doing.
      </>
    ),
  },
  {
    title: 'CPT-ordered dispatch',
    kicker: 'earliest deadline first',
    to: '/docs/overview/task-lifecycle',
    description: (
      <>
        Priority derives entirely from the Critical Pull Time — the deadline by
        which a task must be done for its order to ship. No separate priority
        field, so nothing can drift out of sync with the date that matters.
      </>
    ),
  },
  {
    title: 'SLAM weigh-check',
    kicker: 'label it, or divert it',
    to: '/docs/ddd/aggregates-and-invariants',
    description: (
      <>
        Scan, Label, Apply, Manifest. A carton is never sealed without scanned
        contents, and if its actual weight falls outside tolerance of the
        expected weight it is diverted instead of labelled.
      </>
    ),
  },
];

function Feature({title, kicker, description, to}: FeatureItem) {
  return (
    <div className={clsx('col col--6', styles.featureCol)}>
      <Link to={to} className={styles.featureCard}>
        <span className={styles.featureKicker}>{kicker}</span>
        <Heading as="h3" className={styles.featureTitle}>
          {title}
        </Heading>
        <p className={styles.featureBody}>{description}</p>
      </Link>
    </div>
  );
}

export default function HomepageFeatures(): ReactNode {
  return (
    <section className={styles.features}>
      <div className="container">
        <div className="row">
          {FeatureList.map((props, idx) => (
            <Feature key={idx} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}
