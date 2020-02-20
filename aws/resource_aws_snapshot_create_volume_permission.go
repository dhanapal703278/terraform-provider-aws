package aws

import (
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

func resourceAwsSnapshotCreateVolumePermission() *schema.Resource {
	return &schema.Resource{
		Exists: resourceAwsSnapshotCreateVolumePermissionExists,
		Create: resourceAwsSnapshotCreateVolumePermissionCreate,
		Read:   resourceAwsSnapshotCreateVolumePermissionRead,
		Delete: resourceAwsSnapshotCreateVolumePermissionDelete,

		Schema: map[string]*schema.Schema{
			"snapshot_id": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"account_id": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
		},
	}
}

func resourceAwsSnapshotCreateVolumePermissionExists(d *schema.ResourceData, meta interface{}) (bool, error) {
	conn := meta.(*AWSClient).ec2conn

	snapshotID, accountID, err := resourceAwsSnapshotCreateVolumePermissionParseID(d.Id())
	if err != nil {
		return false, err
	}
	return hasCreateVolumePermission(conn, snapshotID, accountID)
}

func resourceAwsSnapshotCreateVolumePermissionCreate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).ec2conn

	snapshot_id := d.Get("snapshot_id").(string)
	account_id := d.Get("account_id").(string)

	accountIsOwner, err := isAccountSnapshotOwner(conn, snapshot_id, account_id)
	if err != nil {
		return fmt.Errorf("Error adding snapshot %s: %s", ec2.SnapshotAttributeNameCreateVolumePermission, err)
	}

	if accountIsOwner {
		return fmt.Errorf("Error adding snapshot %s: specified account %s is the snapshot owner",
			ec2.SnapshotAttributeNameCreateVolumePermission, account_id)
	}

	_, err = conn.ModifySnapshotAttribute(&ec2.ModifySnapshotAttributeInput{
		SnapshotId: aws.String(snapshot_id),
		Attribute:  aws.String(ec2.SnapshotAttributeNameCreateVolumePermission),
		CreateVolumePermission: &ec2.CreateVolumePermissionModifications{
			Add: []*ec2.CreateVolumePermission{
				{UserId: aws.String(account_id)},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("Error adding snapshot %s: %s", ec2.SnapshotAttributeNameCreateVolumePermission, err)
	}

	d.SetId(fmt.Sprintf("%s-%s", snapshot_id, account_id))

	// Wait for the account to appear in the permission list
	stateConf := &resource.StateChangeConf{
		Pending:    []string{"denied"},
		Target:     []string{"granted"},
		Refresh:    resourceAwsSnapshotCreateVolumePermissionStateRefreshFunc(conn, snapshot_id, account_id),
		Timeout:    20 * time.Minute,
		Delay:      10 * time.Second,
		MinTimeout: 10 * time.Second,
	}
	if _, err := stateConf.WaitForState(); err != nil {
		return fmt.Errorf(
			"Error waiting for snapshot %s (%s) to be added: %s",
			ec2.SnapshotAttributeNameCreateVolumePermission, d.Id(), err)
	}

	return nil
}

func resourceAwsSnapshotCreateVolumePermissionRead(d *schema.ResourceData, meta interface{}) error {
	return nil
}

func resourceAwsSnapshotCreateVolumePermissionDelete(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).ec2conn

	snapshotID, accountID, err := resourceAwsSnapshotCreateVolumePermissionParseID(d.Id())
	if err != nil {
		return err
	}

	_, err = conn.ModifySnapshotAttribute(&ec2.ModifySnapshotAttributeInput{
		SnapshotId: aws.String(snapshotID),
		Attribute:  aws.String(ec2.SnapshotAttributeNameCreateVolumePermission),
		CreateVolumePermission: &ec2.CreateVolumePermissionModifications{
			Remove: []*ec2.CreateVolumePermission{
				{UserId: aws.String(accountID)},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("Error removing snapshot %s: %s", ec2.SnapshotAttributeNameCreateVolumePermission, err)
	}

	// Wait for the account to disappear from the permission list
	stateConf := &resource.StateChangeConf{
		Pending:    []string{"granted"},
		Target:     []string{"denied"},
		Refresh:    resourceAwsSnapshotCreateVolumePermissionStateRefreshFunc(conn, snapshotID, accountID),
		Timeout:    5 * time.Minute,
		Delay:      10 * time.Second,
		MinTimeout: 10 * time.Second,
	}
	if _, err := stateConf.WaitForState(); err != nil {
		return fmt.Errorf(
			"Error waiting for snapshot %s (%s) to be removed: %s",
			ec2.SnapshotAttributeNameCreateVolumePermission, d.Id(), err)
	}

	return nil
}

func isAccountSnapshotOwner(conn *ec2.EC2, snapshot_id string, account_id string) (bool, error) {
	output, err := conn.DescribeSnapshots(&ec2.DescribeSnapshotsInput{
		SnapshotIds: aws.StringSlice([]string{snapshot_id}),
	})
	if err != nil {
		return false, fmt.Errorf("Error describing snapshot %s: %s", snapshot_id, err)
	}

	if len(output.Snapshots) != 1 {
		return false, fmt.Errorf("Error locating snapshot %s: found %d snapshots, expected 1",
			snapshot_id, len(output.Snapshots))
	}

	return *output.Snapshots[0].OwnerId == account_id, nil
}

func hasCreateVolumePermission(conn *ec2.EC2, snapshot_id string, account_id string) (bool, error) {
	_, state, err := resourceAwsSnapshotCreateVolumePermissionStateRefreshFunc(conn, snapshot_id, account_id)()
	if err != nil {
		return false, err
	}
	if state == "granted" {
		return true, nil
	} else {
		return false, nil
	}
}

func resourceAwsSnapshotCreateVolumePermissionStateRefreshFunc(conn *ec2.EC2, snapshot_id string, account_id string) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {
		attrs, err := conn.DescribeSnapshotAttribute(&ec2.DescribeSnapshotAttributeInput{
			SnapshotId: aws.String(snapshot_id),
			Attribute:  aws.String(ec2.SnapshotAttributeNameCreateVolumePermission),
		})
		if err != nil {
			return nil, "", fmt.Errorf("Error refreshing snapshot %s state: %s", ec2.SnapshotAttributeNameCreateVolumePermission, err)
		}

		for _, vp := range attrs.CreateVolumePermissions {
			if aws.StringValue(vp.UserId) == account_id {
				return attrs, "granted", nil
			}
		}
		return attrs, "denied", nil
	}
}

func resourceAwsSnapshotCreateVolumePermissionParseID(id string) (string, string, error) {
	idParts := strings.SplitN(id, "-", 3)
	if len(idParts) != 3 || idParts[0] != "snap" || idParts[1] == "" || idParts[2] == "" {
		return "", "", fmt.Errorf("unexpected format of ID (%s), expected SNAPSHOT_ID-ACCOUNT_ID", id)
	}
	return fmt.Sprintf("%s-%s", idParts[0], idParts[1]), idParts[2], nil
}
