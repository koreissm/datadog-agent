name "datadog-windows-filter-driver.rb"
# at this moment,builds are stored by branch name.  Will need to correct at some point


default_version "db-correct-filter-type"
#
# this should only ever be included by a windows build.
if ohai["platform"] == "windows"
    source :url => "https://s3.amazonaws.com/dd-windowsfilter/builds/dd"
end