const windowsDriveRootPattern = /^([a-z]):[\\/]*$/i;

export interface ManagedBucketPathResolution {
  path: string;
  expandedDriveRoot: boolean;
}

export function resolveManagedBucketPath(
  value: string,
): ManagedBucketPathResolution {
  const path = value.trim();
  const driveRoot = windowsDriveRootPattern.exec(path);
  if (!driveRoot) {
    return { path, expandedDriveRoot: false };
  }

  return {
    path: `${driveRoot[1]!.toUpperCase()}:\\Visto`,
    expandedDriveRoot: true,
  };
}
