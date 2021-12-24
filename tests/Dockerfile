FROM centos:8
RUN yum install -y python3 python3-pip
RUN pip3 install -U pip
RUN pip3 install setuptools
WORKDIR /mnt
COPY requirements.txt /root
RUN cd /root && pip3 install -r requirements.txt
